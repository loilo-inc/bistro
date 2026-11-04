package ldd

import (
	"testing"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

func TestFlattenDepsBreadthFirstAndUnique(t *testing.T) {
	leaf := &Library{Name: "leaf.so", Path: rootfs.MustPath("/lib/leaf.so")}
	// A second object for the same path, as when two requesters resolve it.
	leafAlias := &Library{Name: "alias.so", Path: rootfs.MustPath("/lib/leaf.so")}
	c := &Library{Name: "c.so", Path: rootfs.MustPath("/lib/c.so"), Deps: []*Library{leafAlias}}
	a := &Library{Name: "a.so", Path: rootfs.MustPath("/lib/a.so"), Deps: []*Library{c}}
	b := &Library{Name: "b.so", Path: rootfs.MustPath("/lib/b.so"), Deps: []*Library{leaf}}
	libs, broken := FlattenDeps(a, b, a)
	require.Equal(t, []*Library{a, b, c, leaf}, libs)
	require.Empty(t, broken)
}

func TestFlattenDepsCycle(t *testing.T) {
	a := &Library{Name: "a.so", Path: rootfs.MustPath("/lib/a.so")}
	b := &Library{Name: "b.so", Path: rootfs.MustPath("/lib/b.so"), Deps: []*Library{a}}
	a.Deps = []*Library{b}
	libs, broken := FlattenDeps(a)
	require.Equal(t, []*Library{a, b}, libs)
	require.Empty(t, broken)
}

func TestFlattenDepsBrokenReportedOnce(t *testing.T) {
	a := &Library{Name: "a.so", Path: rootfs.MustPath("/lib/a.so"), Deps: []*Library{{Name: "missing.so"}, {Name: "other.so"}}}
	b := &Library{Name: "b.so", Path: rootfs.MustPath("/lib/b.so"), Deps: []*Library{{Name: "missing.so"}}}
	libs, broken := FlattenDeps(a, b, &Library{Name: "missing.so"})
	require.Equal(t, []*Library{a, b}, libs)
	require.Equal(t, []string{"missing.so", "other.so"}, broken)
}

func TestFlattenDepsResolvedAfterBrokenWithSameName(t *testing.T) {
	// The same DT_NEEDED name can be missing for one requester and resolved
	// for another with a different RPATH or RUNPATH.
	found := &Library{Name: "libfoo.so", Path: rootfs.MustPath("/opt/lib/libfoo.so")}
	a := &Library{Name: "a.so", Path: rootfs.MustPath("/lib/a.so"), Deps: []*Library{{Name: "libfoo.so"}}}
	b := &Library{Name: "b.so", Path: rootfs.MustPath("/lib/b.so"), Deps: []*Library{found}}
	libs, broken := FlattenDeps(a, b)
	require.Equal(t, []*Library{a, b, found}, libs)
	require.Equal(t, []string{"libfoo.so"}, broken)
}

func TestFlattenDepsEmpty(t *testing.T) {
	libs, broken := FlattenDeps()
	require.Nil(t, libs)
	require.Nil(t, broken)
}

func TestLibraryIsBroken(t *testing.T) {
	require.True(t, (&Library{Name: "missing.so"}).IsBroken())
	require.False(t, (&Library{Name: "lib.so", Path: rootfs.MustPath("/lib/lib.so")}).IsBroken())
}
