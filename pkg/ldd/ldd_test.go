package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolverSearchOrder(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		rpath, runpath, env, cache bool
		want                       string
	}{
		{"RPATH before environment", true, false, true, true, "rpath"},
		{"RUNPATH disables RPATH", true, true, true, true, "env"},
		{"RUNPATH before cache", true, true, false, true, "runpath"},
		{"cache before system", false, false, false, true, "cache"},
		{"system fallback", false, false, false, false, "system"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := make(map[string]string)
			for _, name := range []string{"rpath", "env", "runpath", "cache", "system"} {
				paths[name] = filepath.Join(dir, name, "lib.so")
				writeELF(t, paths[name])
			}
			options := ResolverOptions{SysLibraryPath: filepath.Dir(paths["system"])}
			if tt.env {
				options.LdLibraryPath = filepath.Dir(paths["env"])
			}
			var entries []dynamicString
			entries = append(entries, needed("lib.so")...)
			if tt.rpath {
				entries = append(entries, dynamicString{elf.DT_RPATH, filepath.Dir(paths["rpath"])})
			}
			if tt.runpath {
				entries = append(entries, dynamicString{elf.DT_RUNPATH, filepath.Dir(paths["runpath"])})
			}
			var records []cacheRecord
			if tt.cache {
				records = append(records, cacheRecord{"lib.so", paths["cache"], 0x303})
			}
			root := filepath.Join(dir, "app")
			writeELF(t, root, entries...)
			libs, err := testResolver(t, options, records...).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Equal(t, paths[tt.want], libs[0].Path)
		})
	}
}

func TestResolverRPathInheritance(t *testing.T) {
	for _, tt := range []struct{ name, rootTag, childTag, want string }{
		{"inherited RPATH", "rpath", "none", "ancestor"},
		{"nearest RPATH first", "rpath", "rpath", "child"},
		{"RUNPATH is not inherited", "runpath", "none", ""},
		{"child RUNPATH suppresses ancestor", "rpath", "runpath", "child"},
		{"empty child RUNPATH suppresses ancestor", "rpath", "empty-runpath", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			common := filepath.Join(dir, "common")
			ancestor, child := filepath.Join(dir, "ancestor"), filepath.Join(dir, "child")
			writeELF(t, filepath.Join(ancestor, "leaf.so"))
			writeELF(t, filepath.Join(child, "leaf.so"))
			root := filepath.Join(dir, "app")
			rootEntries := needed("mid.so")
			rootPathTag := elf.DT_RPATH
			if tt.rootTag == "runpath" {
				rootPathTag = elf.DT_RUNPATH
			}
			rootEntries = append(rootEntries, dynamicString{rootPathTag, ancestor})
			writeELF(t, root, rootEntries...)
			entries := needed("leaf.so")
			switch tt.childTag {
			case "rpath":
				entries = append(entries, dynamicString{elf.DT_RPATH, child})
			case "runpath":
				entries = append(entries, dynamicString{elf.DT_RUNPATH, child})
			case "empty-runpath":
				entries = append(entries, dynamicString{elf.DT_RUNPATH, ""})
			}
			writeELF(t, filepath.Join(common, "mid.so"), entries...)
			libs, err := testResolver(t, ResolverOptions{SysLibraryPath: common}).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Len(t, libs[0].Deps, 1)
			leaf := libs[0].Deps[0]
			if tt.want == "" {
				require.True(t, leaf.IsBroken())
			} else {
				require.Equal(t, filepath.Join(dir, tt.want, "leaf.so"), leaf.Path)
			}
		})
	}
}

func TestResolverKeepsAncestorAcrossRunPath(t *testing.T) {
	dir := t.TempDir()
	common := filepath.Join(dir, "common")
	b := filepath.Join(dir, "b")
	root := filepath.Join(dir, "app")
	writeELF(t, root, dynamicString{elf.DT_NEEDED, "B.so"}, dynamicString{elf.DT_RPATH, dir})
	writeELF(t, filepath.Join(common, "B.so"), dynamicString{elf.DT_NEEDED, "C.so"}, dynamicString{elf.DT_RUNPATH, b})
	writeELF(t, filepath.Join(b, "C.so"), needed("leaf.so")...)
	writeELF(t, filepath.Join(dir, "leaf.so"))
	libs, err := testResolver(t, ResolverOptions{SysLibraryPath: common}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Len(t, libs[0].Deps, 1)
	require.Len(t, libs[0].Deps[0].Deps, 1)
	require.Equal(t, filepath.Join(dir, "leaf.so"), libs[0].Deps[0].Deps[0].Path)
}

func TestResolverLoadedNamesAndSONAME(t *testing.T) {
	for _, request := range []string{"name", "absolute", "relative"} {
		t.Run(request, func(t *testing.T) {
			dir := t.TempDir()
			a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
			alias := filepath.Join(a, "alias.so")
			writeELF(t, alias, dynamicString{elf.DT_SONAME, "leaf.so"})
			writeELF(t, filepath.Join(b, "leaf.so"))
			writeELF(t, filepath.Join(a, "mid.so"), dynamicString{elf.DT_NEEDED, "leaf.so"}, dynamicString{elf.DT_RUNPATH, b})
			name := "alias.so"
			switch request {
			case "absolute":
				name = alias
			case "relative":
				t.Chdir(dir)
				name = "a/alias.so"
			}
			root := filepath.Join(dir, "app")
			entries := append(needed(name, "mid.so", "leaf.so"), dynamicString{elf.DT_RUNPATH, a})
			writeELF(t, root, entries...)
			libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 3)
			require.Len(t, libs[1].Deps, 1)
			require.Same(t, libs[0], libs[2])
			require.Same(t, libs[0], libs[1].Deps[0])
			require.FileExists(t, libs[0].Path)
		})
	}
}

func TestResolverBreadthFirst(t *testing.T) {
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	root := filepath.Join(dir, "app")
	writeELF(t, root, append(needed("A.so", "B.so"), dynamicString{elf.DT_RUNPATH, a})...)
	writeELF(t, filepath.Join(a, "A.so"), dynamicString{elf.DT_NEEDED, "C.so"}, dynamicString{elf.DT_RUNPATH, a})
	writeELF(t, filepath.Join(a, "B.so"), dynamicString{elf.DT_NEEDED, "leaf.so"}, dynamicString{elf.DT_RUNPATH, b})
	writeELF(t, filepath.Join(a, "C.so"), dynamicString{elf.DT_NEEDED, "leaf.so"}, dynamicString{elf.DT_RUNPATH, c})
	writeELF(t, filepath.Join(b, "leaf.so"))
	writeELF(t, filepath.Join(c, "leaf.so"))
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 2)
	require.Len(t, libs[0].Deps, 1)
	require.Len(t, libs[0].Deps[0].Deps, 1)
	require.Len(t, libs[1].Deps, 1)
	leaf := libs[1].Deps[0]
	require.Equal(t, filepath.Join(b, "leaf.so"), leaf.Path)
	require.Same(t, leaf, libs[0].Deps[0].Deps[0])
}

func TestResolverDoesNotOverwriteSONAME(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	for _, name := range []string{"first.so", "second.so"} {
		writeELF(t, filepath.Join(dir, name), dynamicString{elf.DT_SONAME, "shared.so"})
	}
	writeELF(t, root, append(needed("first.so", "second.so", "shared.so"), dynamicString{elf.DT_RUNPATH, dir})...)
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 3)
	require.Equal(t, filepath.Join(dir, "second.so"), libs[1].Path)
	require.Same(t, libs[0], libs[2])
}

func TestResolverCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	writeELF(t, root, dynamicString{elf.DT_NEEDED, "A.so"}, dynamicString{elf.DT_RUNPATH, dir})
	writeELF(t, filepath.Join(dir, "A.so"), dynamicString{elf.DT_NEEDED, "B.so"}, dynamicString{elf.DT_RUNPATH, dir})
	writeELF(t, filepath.Join(dir, "B.so"), dynamicString{elf.DT_NEEDED, "A.so"}, dynamicString{elf.DT_RUNPATH, dir})
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Len(t, libs[0].Deps, 1)
	require.Len(t, libs[0].Deps[0].Deps, 1)
	require.Same(t, libs[0], libs[0].Deps[0].Deps[0])
}

func TestResolverRootIsolation(t *testing.T) {
	dir := t.TempDir()
	common := filepath.Join(dir, "common")
	writeELF(t, filepath.Join(common, "mid.so"), needed("leaf.so")...)
	r := testResolver(t, ResolverOptions{SysLibraryPath: common})
	for _, name := range []string{"a", "b", "a"} {
		root := filepath.Join(dir, name, "app")
		leaf := filepath.Join(dir, name, "leaf.so")
		writeELF(t, root, dynamicString{elf.DT_NEEDED, "mid.so"}, dynamicString{elf.DT_RPATH, "$ORIGIN"})
		writeELF(t, leaf)
		libs, err := r.ListObjects(root)
		require.NoError(t, err)
		require.Len(t, libs, 1)
		require.Len(t, libs[0].Deps, 1)
		require.Equal(t, leaf, libs[0].Deps[0].Path)
	}
}

func TestResolverBrokenAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeELF(t, "libs/real.so")
	require.NoError(t, os.Symlink("real.so", "libs/link.so"))
	require.NoError(t, os.Mkdir("libs/directory.so", 0755))
	root := filepath.Join(dir, "app")
	writeELF(t, root, append(needed("link.so", "directory.so", "missing.so", "./absent.so", filepath.Join(dir, "absent.so")), dynamicString{elf.DT_RUNPATH, "libs"})...)
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 5)
	abs, err := filepath.Abs("libs/link.so")
	require.NoError(t, err)
	require.Equal(t, abs, libs[0].Path)
	for _, lib := range libs[1:] {
		require.True(t, lib.IsBroken(), lib.Name)
		require.Empty(t, lib.Deps)
	}
}

func TestResolverErrorsAndNoDependencies(t *testing.T) {
	r := testResolver(t, ResolverOptions{})
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	_, err := r.ListObjects(root)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(root, []byte("not ELF"), 0644))
	_, err = r.ListObjects(root)
	require.Error(t, err)
	writeELF(t, root)
	libs, err := r.ListObjects(root)
	require.NoError(t, err)
	require.Empty(t, libs)
	writeELF(t, filepath.Join(dir, "invalid.so"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "invalid.so"), []byte("not ELF"), 0644))
	writeELF(t, root, dynamicString{elf.DT_NEEDED, "invalid.so"}, dynamicString{elf.DT_RUNPATH, dir})
	_, err = r.ListObjects(root)
	require.Error(t, err)
}

func TestResolverLibraryPathVariables(t *testing.T) {
	dir := t.TempDir()
	mid := filepath.Join(dir, "plugins", "mid.so")
	writeELF(t, mid, needed("leaf.so")...)
	// A decoy alongside the shared library catches expansion against the
	// importing library instead of the root executable.
	writeELF(t, filepath.Join(filepath.Dir(mid), "libs", "x86_64", "leaf.so"))
	r := testResolver(t, ResolverOptions{
		LdLibraryPath: "$ORIGIN/${LIB}/$PLATFORM",
		Lib:           "libs", Platform: "x86_64",
	})
	for _, name := range []string{"a", "b", "a"} {
		rootDir := filepath.Join(dir, name)
		root := filepath.Join(rootDir, "app")
		leaf := filepath.Join(rootDir, "libs", "x86_64", "leaf.so")
		writeELF(t, root, needed(mid)...)
		writeELF(t, leaf)
		libs, err := r.ListObjects(root)
		require.NoError(t, err)
		require.Len(t, libs, 1)
		require.Len(t, libs[0].Deps, 1)
		require.Equal(t, leaf, libs[0].Deps[0].Path)
	}
}

func TestResolverNeededPathVariables(t *testing.T) {
	dir := t.TempDir()
	midDir := filepath.Join(dir, "plugins")
	mid := filepath.Join(midDir, "mid.so")
	leaf := filepath.Join(midDir, "libs", "x86_64", "leaf.so")
	writeELF(t, leaf)
	writeELF(t, mid, needed("${ORIGIN}/$LIB/${PLATFORM}/leaf.so")...)
	root := filepath.Join(dir, "app")
	writeELF(t, root, needed(mid)...)
	r := testResolver(t, ResolverOptions{Lib: "libs", Platform: "x86_64"})
	libs, err := r.ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Len(t, libs[0].Deps, 1)
	require.Equal(t, leaf, libs[0].Deps[0].Path,
		"DT_NEEDED ORIGIN belongs to the importing ELF, unlike LD_LIBRARY_PATH")
}

func TestResolverNeededVariablesReuseLoadedLibrary(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	first := filepath.Join(a, "libx86_64.so")
	writeELF(t, first)
	writeELF(t, filepath.Join(b, "libx86_64.so"))
	writeELF(t, filepath.Join(a, "mid.so"),
		dynamicString{elf.DT_NEEDED, "lib${PLATFORM}.so"},
		dynamicString{elf.DT_RUNPATH, b},
	)
	root := filepath.Join(dir, "app")
	writeELF(t, root, append(needed("libx86_64.so", "mid.so"),
		dynamicString{elf.DT_RUNPATH, a})...)
	libs, err := testResolver(t, ResolverOptions{Platform: "x86_64"}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 2)
	require.Len(t, libs[1].Deps, 1)
	require.Equal(t, first, libs[0].Path)
	require.Same(t, libs[0], libs[1].Deps[0],
		"expand DT_NEEDED before consulting the loaded-library cache")
}
