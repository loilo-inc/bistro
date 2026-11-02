package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

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
	r := withSysLibraryPaths(testResolver(t, ResolverOptions{}), common)
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

func TestResolverErrorsAndNoDependencies(t *testing.T) {
	r := testResolver(t, ResolverOptions{})
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	_, err := r.ListObjects(root)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(root, []byte("not ELF"), 0644))
	_, err = r.ListObjects(root)
	require.ErrorContains(t, err, "[ldd/readElf] malformed ELF file: "+root+": ")
	var formatErr *elf.FormatError
	require.ErrorAs(t, err, &formatErr)
	writeELF(t, root)
	libs, err := r.ListObjects(root)
	require.NoError(t, err)
	require.Empty(t, libs)
}

func TestNewResolverSystemPaths(t *testing.T) {
	r := NewResolver(ResolverOptions{DstLib: "lib/x86_64-linux-gnu"}).(*resolver)
	require.Equal(t, []string{
		"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu",
		"/lib", "/usr/lib", "/lib64", "/usr/lib64",
	}, r.sysLibraryPaths)
	r = NewResolver(ResolverOptions{}).(*resolver)
	require.Equal(t, []string{"/lib", "/usr/lib", "/lib64", "/usr/lib64"}, r.sysLibraryPaths,
		"an empty $LIB must not add / or /usr as search paths")
}

func TestDefaultResolver(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "/opt/lib")
	r := DefaultResolver().(*resolver)
	require.Equal(t, "/opt/lib", r.ldLibraryPath)
	require.Equal(t, getDstLib(runtime.GOARCH), r.dstLib)
	require.Equal(t, getDstPlatform(runtime.GOARCH), r.dstPlatform)
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
		DstLib:        "libs", DstPlatform: "x86_64",
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
