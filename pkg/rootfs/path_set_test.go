package rootfs

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestPathSetMatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   string
		excludes []string
		want     bool
	}{
		{name: "no exclusions", target: "/app/cache/tool"},
		{name: "exact match", target: "/app/cache", excludes: []string{"/app/cache"}, want: true},
		{name: "descendant", target: "/app/cache/nested/tool", excludes: []string{"/app/cache"}, want: true},
		{name: "sibling directory", target: "/app/cache-old/tool", excludes: []string{"/app/cache"}},
		{name: "sibling file", target: "/app/cache.txt", excludes: []string{"/app/cache"}},
		{name: "ancestor", target: "/app", excludes: []string{"/app/cache"}},
		{name: "trailing slash", target: "/app/cache/tool", excludes: []string{"/app/cache/"}, want: true},
		{name: "root descendant", target: "/app/tool", excludes: []string{"/"}, want: true},
		{name: "root exact match", target: "/", excludes: []string{"/"}, want: true},
		{name: "multiple exclusions", target: "/app/cache/tool", excludes: []string{"/other", "/app/cache"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := NewPathSet(MustPaths(tc.excludes...))
			fss := fstest.MapFS{
				"app/cache/nested/tool": {},
				"app/cache/tool":        {},
				"app/cache-old/tool":    {},
				"app/cache.txt":         {},
				"app/tool":              {},
				"other":                 {Mode: fs.ModeDir | 0755},
			}
			for range 2 {
				got, err := ps.Match(fss, MustPath(tc.target))
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func pathSetFixture() fstest.MapFS {
	return fstest.MapFS{
		"alias":               {Mode: fs.ModeSymlink | 0777, Data: []byte("real")},
		"other":               {Mode: fs.ModeSymlink | 0777, Data: []byte("real")},
		"real/cache/file":     {Data: []byte("cache")},
		"real/cache-old/file": {Data: []byte("keep")},
		"real/keep":           {Data: []byte("keep")},
		"real/link":           {Mode: fs.ModeSymlink | 0777, Data: []byte("keep")},
		"cycle":               {Mode: fs.ModeSymlink | 0777, Data: []byte("cycle")},
	}
}

func TestPathSetMatchAliases(t *testing.T) {
	for _, tc := range []struct {
		name, exclude, target string
		want                  bool
	}{
		{"original", "/alias/cache", "/alias/cache/file", true},
		{"resolved target", "/alias/cache", "/real/cache/file", true},
		{"resolved exclusion", "/real/cache", "/alias/cache/file", true},
		{"different aliases", "/alias/cache", "/other/cache/file", true},
		{"exact canonical path", "/alias/cache/file", "/real/cache/file", true},
		{"sibling prefix", "/alias/cache", "/real/cache-old/file", false},
		{"exclude link only", "/real/link", "/real/keep", false},
		{"excluded cycle", "/cycle", "/cycle", true},
		{"missing exclusion parent", "/missing/cache", "/real/keep", false},
		{"missing target", "/real/cache", "/missing/file", false},
		{"root", "/", "/real/keep", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := NewPathSet(MustPaths(tc.exclude))
			for range 2 {
				got, err := ps.Match(pathSetFixture(), MustPath(tc.target))
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

type countingPathFS struct {
	fs.FS
	stats map[string]int
}

func (f countingPathFS) Lstat(name string) (fs.FileInfo, error) {
	f.stats[name]++
	return fs.Lstat(f.FS, name)
}
func (f countingPathFS) ReadLink(name string) (string, error) { return fs.ReadLink(f.FS, name) }

func TestPathSetCloneAndAddPreserveResolution(t *testing.T) {
	fss := countingPathFS{FS: pathSetFixture(), stats: map[string]int{}}
	ps := NewPathSet(MustPaths("/alias/cache", "/real/keep"))
	require.NoError(t, ps.Resolve(fss))
	require.Equal(t, MustPaths("/real/cache"), ps.resolvedPaths, "only aliases need extra paths")
	before := fss.stats["alias"]
	clone := ps.Clone()
	require.NoError(t, clone.Resolve(fss))
	require.Equal(t, before, fss.stats["alias"], "clone must retain resolution progress")
	clone.Add(MustPath("/other/keep"))
	require.NoError(t, clone.Resolve(fss))
	require.Equal(t, before, fss.stats["alias"], "adding a path must not resolve prior paths again")
	require.Equal(t, MustPaths("/real/cache", "/real/keep"), clone.resolvedPaths)
	require.Equal(t, MustPaths("/real/cache"), ps.resolvedPaths)
	require.Len(t, ps.origPaths, 2)
	ps.Add(MustPath("/cycle"))
	require.Len(t, clone.origPaths, 3)
	require.Equal(t, MustPath("/other/keep"), clone.origPaths[2], "clone must own its backing slices")
}

func TestPathSetResolutionErrors(t *testing.T) {
	t.Run("candidate", func(t *testing.T) {
		ps := NewPathSet(nil)
		_, err := ps.Match(lstatErrorFS{FS: pathSetFixture(), cause: fs.ErrPermission}, MustPath("/target"))
		require.ErrorIs(t, err, fs.ErrPermission)
	})
	t.Run("exclusion parent and retry", func(t *testing.T) {
		fss := fstest.MapFS{"alias": {Mode: fs.ModeSymlink | 0777, Data: []byte("target")}, "target/file": {}}
		ps := NewPathSet(MustPaths("/missing/file", "/alias/file"))
		require.ErrorIs(t, ps.Resolve(lstatErrorFS{FS: fss, cause: fs.ErrPermission}), fs.ErrPermission)
		require.Equal(t, 1, ps.resolvedIdx)
		require.NoError(t, ps.Resolve(fss))
		require.Equal(t, 2, ps.resolvedIdx)
		require.Equal(t, MustPaths("/target/file"), ps.resolvedPaths)
	})
}

func TestPathSetStopsAtExcludedSymlinkTargets(t *testing.T) {
	for _, tc := range []struct {
		name string
		fss  fs.FS
	}{
		{name: "circular link inside excluded directory", fss: fstest.MapFS{
			"link":        {Mode: fs.ModeSymlink | 0777, Data: []byte("/target/loop")},
			"target/loop": {Mode: fs.ModeSymlink | 0777, Data: []byte("loop")},
		}},
		{name: "permission denied inside excluded directory", fss: lstatErrorFS{FS: fstest.MapFS{
			"link":        {Mode: fs.ModeSymlink | 0777, Data: []byte("/target/file")},
			"target/file": {},
		}, cause: fs.ErrPermission}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := NewPathSet(MustPaths("/target"))
			for range 2 {
				matched, err := ps.Match(tc.fss, MustPath("/link"))
				require.NoError(t, err)
				require.True(t, matched)
			}
			// The same target must still report an error when it is not excluded.
			unexcluded := NewPathSet(nil)
			_, err := unexcluded.Match(tc.fss, MustPath("/link"))
			require.Error(t, err)
		})
	}
}

func TestPathSetMatchesCanonicalExcludedLinksBeforeResolution(t *testing.T) {
	for _, target := range []string{"keep", "missing", "link"} {
		t.Run(target, func(t *testing.T) {
			fss := fstest.MapFS{
				"alias":     {Mode: fs.ModeSymlink | 0777, Data: []byte("real")},
				"real/link": {Mode: fs.ModeSymlink | 0777, Data: []byte(target)},
				"real/keep": {Data: []byte("keep")},
			}
			ps := NewPathSet(MustPaths("/alias/link"))
			for range 2 {
				matched, err := ps.Match(fss, MustPath("/real/link"))
				require.NoError(t, err)
				require.True(t, matched, "the canonical excluded path must match without following its final link")
			}
			matched, err := ps.Match(fss, MustPath("/real/keep"))
			require.NoError(t, err)
			require.False(t, matched, "excluding a link must not exclude its target")
		})
	}
}
