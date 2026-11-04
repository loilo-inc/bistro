package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

func TestResolverBreadthFirst(t *testing.T) {
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, append(elftest.Needed("A.so", "B.so"), elftest.DynString(elf.DT_RUNPATH, a))...)
	elftest.WriteDynamic(t, filepath.Join(a, "A.so"), elftest.DynString(elf.DT_NEEDED, "C.so"), elftest.DynString(elf.DT_RUNPATH, a))
	elftest.WriteDynamic(t, filepath.Join(a, "B.so"), elftest.DynString(elf.DT_NEEDED, "leaf.so"), elftest.DynString(elf.DT_RUNPATH, b))
	elftest.WriteDynamic(t, filepath.Join(a, "C.so"), elftest.DynString(elf.DT_NEEDED, "leaf.so"), elftest.DynString(elf.DT_RUNPATH, c))
	elftest.WriteDynamic(t, filepath.Join(b, "leaf.so"))
	elftest.WriteDynamic(t, filepath.Join(c, "leaf.so"))
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(rootfs.NewPath(root))
	require.NoError(t, err)
	require.Len(t, libs, 2)
	require.Len(t, libs[0].Deps, 1)
	require.Len(t, libs[0].Deps[0].Deps, 1)
	require.Len(t, libs[1].Deps, 1)
	leaf := libs[1].Deps[0]
	require.Equal(t, filepath.Join(b, "leaf.so"), leaf.Path.Abs())
	require.Same(t, leaf, libs[0].Deps[0].Deps[0])
}

func TestResolverCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, elftest.DynString(elf.DT_NEEDED, "A.so"), elftest.DynString(elf.DT_RUNPATH, dir))
	elftest.WriteDynamic(t, filepath.Join(dir, "A.so"), elftest.DynString(elf.DT_NEEDED, "B.so"), elftest.DynString(elf.DT_RUNPATH, dir))
	elftest.WriteDynamic(t, filepath.Join(dir, "B.so"), elftest.DynString(elf.DT_NEEDED, "A.so"), elftest.DynString(elf.DT_RUNPATH, dir))
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(rootfs.NewPath(root))
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Len(t, libs[0].Deps, 1)
	require.Len(t, libs[0].Deps[0].Deps, 1)
	require.Same(t, libs[0], libs[0].Deps[0].Deps[0])
}

func TestResolverRootIsolation(t *testing.T) {
	dir := t.TempDir()
	common := filepath.Join(dir, "common")
	elftest.WriteDynamic(t, filepath.Join(common, "mid.so"), elftest.Needed("leaf.so")...)
	r := withSysLibraryPaths(testResolver(t, ResolverOptions{}), common)
	for _, name := range []string{"a", "b", "a"} {
		root := filepath.Join(dir, name, "app")
		leaf := filepath.Join(dir, name, "leaf.so")
		elftest.WriteDynamic(t, root, elftest.DynString(elf.DT_NEEDED, "mid.so"), elftest.DynString(elf.DT_RPATH, "$ORIGIN"))
		elftest.WriteDynamic(t, leaf)
		libs, err := r.ListObjects(rootfs.NewPath(root))
		require.NoError(t, err)
		require.Len(t, libs, 1)
		require.Len(t, libs[0].Deps, 1)
		require.Equal(t, leaf, libs[0].Deps[0].Path.Abs())
	}
}

func TestResolverErrorsAndNoDependencies(t *testing.T) {
	r := testResolver(t, ResolverOptions{})
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	_, err := r.ListObjects(rootfs.NewPath(root))
	require.ErrorIs(t, err, os.ErrNotExist)
	elftest.WriteNotELF(t, root)
	_, err = r.ListObjects(rootfs.NewPath(root))
	require.ErrorContains(t, err, "[ldd/readElf] malformed ELF file: "+root+": ")
	var formatErr *elf.FormatError
	require.ErrorAs(t, err, &formatErr)
	elftest.WriteDynamic(t, root)
	libs, err := r.ListObjects(rootfs.NewPath(root))
	require.NoError(t, err)
	require.Empty(t, libs)
}

func TestNewResolverSystemPaths(t *testing.T) {
	r := NewResolver("/", ResolverOptions{DstLib: "lib/x86_64-linux-gnu"}).(*resolver)
	require.Equal(t, rootfs.Paths("/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu",
		"/lib", "/usr/lib", "/lib64", "/usr/lib64"), r.sysLibraryPaths)
	r = NewResolver("/", ResolverOptions{}).(*resolver)
	want := rootfs.Paths("/lib", "/usr/lib", "/lib64", "/usr/lib64")
	if lib := getDstLib(runtime.GOARCH); lib != "" {
		want = append(rootfs.Paths("/"+lib, "/usr/"+lib), want...)
	}
	require.Equal(t, want, r.sysLibraryPaths)
}

func TestNewResolverOptions(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "/opt/lib")
	cwd, err := os.Getwd()
	require.NoError(t, err)
	r := NewResolver(cwd, ResolverOptions{LdLibraryPath: os.Getenv("LD_LIBRARY_PATH")}).(*resolver)
	require.Equal(t, "/opt/lib", r.ldLibraryPath)
	require.Equal(t, getDstLib(runtime.GOARCH), r.dstLib)
	require.Equal(t, getDstPlatform(runtime.GOARCH), r.dstPlatform)
}

func TestResolverLibraryPathVariables(t *testing.T) {
	dir := t.TempDir()
	mid := filepath.Join(dir, "plugins", "mid.so")
	elftest.WriteDynamic(t, mid, elftest.Needed("leaf.so")...)
	// A decoy alongside the shared library catches expansion against the
	// importing library instead of the root executable.
	elftest.WriteDynamic(t, filepath.Join(filepath.Dir(mid), "libs", "x86_64", "leaf.so"))
	r := testResolver(t, ResolverOptions{
		LdLibraryPath: "$ORIGIN/${LIB}/$PLATFORM",
		DstLib:        "libs", DstPlatform: "x86_64",
	})
	for _, name := range []string{"a", "b", "a"} {
		rootDir := filepath.Join(dir, name)
		root := filepath.Join(rootDir, "app")
		leaf := filepath.Join(rootDir, "libs", "x86_64", "leaf.so")
		elftest.WriteDynamic(t, root, elftest.Needed(mid)...)
		elftest.WriteDynamic(t, leaf)
		libs, err := r.ListObjects(rootfs.NewPath(root))
		require.NoError(t, err)
		require.Len(t, libs, 1)
		require.Len(t, libs[0].Deps, 1)
		require.Equal(t, leaf, libs[0].Deps[0].Path.Abs())
	}
}
