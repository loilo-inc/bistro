package rootfs

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestTraverserVisitsEachPathOnceAcrossWalkAndTraverse(t *testing.T) {
	fss := &traversalBudgetFS{FS: fstest.MapFS{
		"tree/file":     {Data: []byte("asset")},
		"tree/self":     {Mode: fs.ModeSymlink | 0777, Data: []byte(".")},
		"tree/external": {Mode: fs.ModeSymlink | 0777, Data: []byte("../outside")},
		"outside/file":  {Data: []byte("external asset")},
		"outside/back":  {Mode: fs.ModeSymlink | 0777, Data: []byte("../tree")},
		"first":         {Mode: fs.ModeSymlink | 0777, Data: []byte("tree")},
		"second":        {Mode: fs.ModeSymlink | 0777, Data: []byte("tree")},
	}}
	visits := map[Path]int{}
	links := map[Path]string{}
	tr := NewTraverser(fss, nil, func(p Path, link string, _ fs.FileInfo) error {
		visits[p]++
		if link != "" {
			links[p] = link
		}
		return nil
	})
	require.NoError(t, tr.Walk(MustPath("/tree")))
	require.NotContains(t, visits, MustPath("/tree/file"), "Walk must not enumerate directory contents")
	require.NoError(t, tr.Walk(MustPath("/tree/file")))
	require.NoError(t, tr.Traverse(MustPath("/first")))
	require.NoError(t, tr.Traverse(MustPath("/second")))
	require.NoError(t, tr.Traverse(MustPath("/tree")))
	require.NoError(t, tr.Walk(MustPath("/second")))
	for p, n := range visits {
		require.Equal(t, 1, n, p.Abs())
	}
	for name := range map[string]bool{"/tree/file": true, "/outside/file": true, "/tree/self": true, "/outside/back": true} {
		require.Equal(t, 1, visits[MustPath(name)], name)
	}
	require.Equal(t, "tree", links[MustPath("/first")])
	require.Equal(t, "tree", links[MustPath("/second")], "new aliases must be visited even if their destination was already traversed")
}

func TestTraverserRetriesFailedVisit(t *testing.T) {
	fss := fstest.MapFS{"file": {Data: []byte("asset")}}
	failure := errors.New("visit failed")
	calls := 0
	tr := NewTraverser(fss, nil, func(p Path, _ string, _ fs.FileInfo) error {
		if p == MustPath("/file") {
			calls++
			if calls == 1 {
				return failure
			}
		}
		return nil
	})
	require.ErrorIs(t, tr.Walk(MustPath("/file")), failure)
	require.NoError(t, tr.Walk(MustPath("/file")))
	require.NoError(t, tr.Traverse(MustPath("/file")))
	require.Equal(t, 2, calls, "only successful visits should suppress later callbacks")
}
