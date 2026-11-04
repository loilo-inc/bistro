package ldd

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/rootfs"
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
			elftest.WriteDynamic(t, filepath.Join(ancestor, "leaf.so"))
			elftest.WriteDynamic(t, filepath.Join(child, "leaf.so"))
			root := filepath.Join(dir, "app")
			rootEntries := elftest.Needed("mid.so")
			rootPathTag := elf.DT_RPATH
			if tt.rootTag == "runpath" {
				rootPathTag = elf.DT_RUNPATH
			}
			rootEntries = append(rootEntries, elftest.DynString(rootPathTag, ancestor))
			elftest.WriteDynamic(t, root, rootEntries...)
			entries := elftest.Needed("leaf.so")
			switch tt.childTag {
			case "rpath":
				entries = append(entries, elftest.DynString(elf.DT_RPATH, child))
			case "runpath":
				entries = append(entries, elftest.DynString(elf.DT_RUNPATH, child))
			case "empty-runpath":
				entries = append(entries, elftest.DynString(elf.DT_RUNPATH, ""))
			}
			elftest.WriteDynamic(t, filepath.Join(common, "mid.so"), entries...)
			libs, err := withSysLibraryPaths(testResolver(t, ResolverOptions{}), common).ListObjects(rootfs.MustPath(root))
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Len(t, libs[0].Deps, 1)
			leaf := libs[0].Deps[0]
			if tt.want == "" {
				require.True(t, leaf.IsBroken())
			} else {
				require.Equal(t, filepath.Join(dir, tt.want, "leaf.so"), leaf.Path.Abs())
			}
		})
	}
}

func TestResolverKeepsAncestorAcrossRunPath(t *testing.T) {
	dir := t.TempDir()
	common := filepath.Join(dir, "common")
	b := filepath.Join(dir, "b")
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, elftest.DynString(elf.DT_NEEDED, "B.so"), elftest.DynString(elf.DT_RPATH, dir))
	elftest.WriteDynamic(t, filepath.Join(common, "B.so"), elftest.DynString(elf.DT_NEEDED, "C.so"), elftest.DynString(elf.DT_RUNPATH, b))
	elftest.WriteDynamic(t, filepath.Join(b, "C.so"), elftest.Needed("leaf.so")...)
	elftest.WriteDynamic(t, filepath.Join(dir, "leaf.so"))
	libs, err := withSysLibraryPaths(testResolver(t, ResolverOptions{}), common).ListObjects(rootfs.MustPath(root))
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Len(t, libs[0].Deps, 1)
	require.Len(t, libs[0].Deps[0].Deps, 1)
	require.Equal(t, filepath.Join(dir, "leaf.so"), libs[0].Deps[0].Deps[0].Path.Abs())
}

func TestResolverLoadedNamesAndSONAME(t *testing.T) {
	for _, request := range []string{"name", "absolute", "relative"} {
		t.Run(request, func(t *testing.T) {
			dir := t.TempDir()
			a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
			alias := filepath.Join(a, "alias.so")
			elftest.WriteDynamic(t, alias, elftest.DynString(elf.DT_SONAME, "leaf.so"))
			elftest.WriteDynamic(t, filepath.Join(b, "leaf.so"))
			elftest.WriteDynamic(t, filepath.Join(a, "mid.so"), elftest.DynString(elf.DT_NEEDED, "leaf.so"), elftest.DynString(elf.DT_RUNPATH, b))
			name := "alias.so"
			switch request {
			case "absolute":
				name = alias
			case "relative":
				t.Chdir(dir)
				name = "a/alias.so"
			}
			root := filepath.Join(dir, "app")
			entries := append(elftest.Needed(name, "mid.so", "leaf.so"), elftest.DynString(elf.DT_RUNPATH, a))
			elftest.WriteDynamic(t, root, entries...)
			libs, err := testResolver(t, ResolverOptions{}).ListObjects(rootfs.MustPath(root))
			require.NoError(t, err)
			require.Len(t, libs, 3)
			require.Len(t, libs[1].Deps, 1)
			require.Same(t, libs[0], libs[2])
			require.Same(t, libs[0], libs[1].Deps[0])
			require.FileExists(t, libs[0].Path.Abs())
		})
	}
}

func TestResolverDoesNotOverwriteSONAME(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	for _, name := range []string{"first.so", "second.so"} {
		elftest.WriteDynamic(t, filepath.Join(dir, name), elftest.DynString(elf.DT_SONAME, "shared.so"))
	}
	elftest.WriteDynamic(t, root, append(elftest.Needed("first.so", "second.so", "shared.so"), elftest.DynString(elf.DT_RUNPATH, dir))...)
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(rootfs.MustPath(root))
	require.NoError(t, err)
	require.Len(t, libs, 3)
	require.Equal(t, filepath.Join(dir, "second.so"), libs[1].Path.Abs())
	require.Same(t, libs[0], libs[2])
}

func TestResolverNeededPathVariables(t *testing.T) {
	dir := t.TempDir()
	midDir := filepath.Join(dir, "plugins")
	mid := filepath.Join(midDir, "mid.so")
	leaf := filepath.Join(midDir, "libs", "x86_64", "leaf.so")
	elftest.WriteDynamic(t, leaf)
	elftest.WriteDynamic(t, mid, elftest.Needed("${ORIGIN}/$LIB/${PLATFORM}/leaf.so")...)
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, elftest.Needed(mid)...)
	r := testResolver(t, ResolverOptions{DstLib: "libs", DstPlatform: "x86_64"})
	libs, err := r.ListObjects(rootfs.MustPath(root))
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Len(t, libs[0].Deps, 1)
	require.Equal(t, leaf, libs[0].Deps[0].Path.Abs(),
		"DT_NEEDED ORIGIN belongs to the importing ELF, unlike LD_LIBRARY_PATH")
}

func TestResolverNeededVariablesReuseLoadedLibrary(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	first := filepath.Join(a, "libx86_64.so")
	elftest.WriteDynamic(t, first)
	elftest.WriteDynamic(t, filepath.Join(b, "libx86_64.so"))
	elftest.WriteDynamic(t, filepath.Join(a, "mid.so"),
		elftest.DynString(elf.DT_NEEDED, "lib${PLATFORM}.so"),
		elftest.DynString(elf.DT_RUNPATH, b),
	)
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, append(elftest.Needed("libx86_64.so", "mid.so"),
		elftest.DynString(elf.DT_RUNPATH, a))...)
	libs, err := testResolver(t, ResolverOptions{DstPlatform: "x86_64"}).ListObjects(rootfs.MustPath(root))
	require.NoError(t, err)
	require.Len(t, libs, 2)
	require.Len(t, libs[1].Deps, 1)
	require.Equal(t, first, libs[0].Path.Abs())
	require.Same(t, libs[0], libs[1].Deps[0],
		"expand DT_NEEDED before consulting the loaded-library cache")
}

func TestResolverMissingInterpreter(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	loader := filepath.Join(dir, "missing-loader")
	elftest.Write(t, app, elftest.Fixture{Type: elf.ET_EXEC, Interp: loader})
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(rootfs.MustPath(app))
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Equal(t, loader, libs[0].Name)
	require.True(t, libs[0].IsBroken())
}

func TestResolverInterpreterSONAME(t *testing.T) {
	for _, competingLoader := range []bool{false, true} {
		t.Run(fmt.Sprintf("competing loader=%t", competingLoader), func(t *testing.T) {
			dir := t.TempDir()
			app := filepath.Join(dir, "app")
			loader := filepath.Join(dir, "custom", "loader")
			libDir := filepath.Join(dir, "lib")
			const soname = "ld-test.so"
			elftest.WriteDynamic(t, loader, elftest.DynString(elf.DT_SONAME, soname))
			if competingLoader {
				elftest.WriteDynamic(t, filepath.Join(libDir, soname), elftest.DynString(elf.DT_SONAME, soname))
			}
			elftest.Write(t, app, elftest.Fixture{
				Type: elf.ET_EXEC, Interp: loader,
				Entries: append(elftest.Needed(soname), elftest.DynString(elf.DT_RUNPATH, libDir)),
			})
			libs, err := withSysLibraryPaths(testResolver(t, ResolverOptions{}), libDir).ListObjects(rootfs.MustPath(app))
			require.NoError(t, err)
			require.Len(t, libs, 2)
			require.Equal(t, loader, libs[0].Path.Abs())
			require.Same(t, libs[0], libs[1], "DT_NEEDED must reuse the interpreter by SONAME")
		})
	}
}

func TestReadElfCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.so")
	elftest.WriteDynamic(t, path)
	r := &rootResolver{resolver: NewResolver(ResolverOptions{Cwd: "/", FS: os.DirFS("/")}).(*resolver)}
	first, err := r.readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	second, err := r.readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Same(t, first, second, "cached metadata must not reopen the file")
}

func TestRootResolverReadElfDoesNotCacheErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.so")
	r := &rootResolver{resolver: NewResolver(ResolverOptions{Cwd: "/", FS: os.DirFS("/")}).(*resolver)}
	_, err := r.readElf(rootfs.MustPath(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	elftest.WriteNotELF(t, path)
	_, err = r.readElf(rootfs.MustPath(path))
	var formatErr *elf.FormatError
	require.ErrorAs(t, err, &formatErr)
	elftest.WriteDynamic(t, path)
	info, err := r.readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Equal(t, path, info.Path.Abs())
}
