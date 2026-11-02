package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
			libs, err := withSysLibraryPaths(testResolver(t, ResolverOptions{}), common).ListObjects(root)
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
	libs, err := withSysLibraryPaths(testResolver(t, ResolverOptions{}), common).ListObjects(root)
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

func TestReadElfCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.so")
	writeELF(t, path)
	r := &rootResolver{resolver: NewResolver(ResolverOptions{})}
	first, err := r.readElf(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	second, err := r.readElf(path)
	require.NoError(t, err)
	require.Same(t, first, second, "cached metadata must not reopen the file")
}

func TestRootResolverReadElfDoesNotCacheErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.so")
	r := &rootResolver{resolver: NewResolver(ResolverOptions{})}
	_, err := r.readElf(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(path, []byte("not ELF"), 0644))
	_, err = r.readElf(path)
	var formatErr *elf.FormatError
	require.ErrorAs(t, err, &formatErr)
	writeELF(t, path)
	info, err := r.readElf(path)
	require.NoError(t, err)
	require.Equal(t, path, info.Path)
}
