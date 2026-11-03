package rootfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSymLinkResolverResolve(t *testing.T) {
	root := t.TempDir()
	writeRootFSFile(t, filepath.Join(root, "usr/bin/tool"), "tool", 0755)
	writeRootFSFile(t, filepath.Join(root, "usr/bin/other"), "other", 0755)
	for name, target := range map[string]string{
		"bin":              "usr/bin",
		"absolute":         "/usr/bin/tool",
		"chain":            "absolute",
		"usr/bin/relative": "tool",
		"selfdir":          ".",
		"broken":           "missing",
		"cycle":            "cycle",
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
	}
	resolver := NewSymLinkResolver(os.DirFS(root))
	// Resolve the parent before its children to catch suffix loss across calls.
	for _, tc := range []struct{ input, want string }{
		{"/usr/bin/tool", "/usr/bin/tool"},
		{"/bin", "/usr/bin"},
		{"/bin/tool", "/usr/bin/tool"},
		{"/bin/other", "/usr/bin/other"},
		{"/absolute", "/usr/bin/tool"},
		{"/chain", "/usr/bin/tool"},
		{"/usr/bin/relative", "/usr/bin/tool"},
		{"/selfdir/selfdir/usr/bin/tool", "/usr/bin/tool"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for range 2 {
				resolved, err := resolver.Resolve(NewPath(tc.input))
				require.NoError(t, err)
				require.NotNil(t, resolved)
				require.Equal(t, tc.want, resolved.Abs())
			}
		})
	}
	for range 2 {
		_, err := resolver.Resolve(NewPath("/broken"))
		require.ErrorIs(t, err, fs.ErrNotExist)
		_, err = resolver.Resolve(NewPath("/cycle"))
		require.ErrorContains(t, err, "circular symlink")
	}
	// A failed traversal must leave the resolver usable.
	resolved, err := resolver.Resolve(NewPath("/bin/tool"))
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/tool", resolved.Abs())
}

func TestSymLinkResolverVisitSkipsTargetWithoutAffectingResolve(t *testing.T) {
	root := t.TempDir()
	writeRootFSFile(t, filepath.Join(root, "target"), "content", 0644)
	require.NoError(t, os.Symlink("/target", filepath.Join(root, "alias")))
	resolver := NewSymLinkResolver(os.DirFS(root))
	var visited []string
	err := resolver.Visit(NewPath("/alias"), func(p Path) bool {
		return p.Abs() != "/target"
	}, func(p Path, link string, info fs.FileInfo) error {
		visited = append(visited, p.Abs())
		if p.Abs() == "/alias" {
			require.Equal(t, "/target", link)
			require.NotZero(t, info.Mode()&fs.ModeSymlink)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"/", "/alias"}, visited)
	resolved, err := resolver.Resolve(NewPath("/alias"))
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, "/target", resolved.Abs())
}

func TestSymLinkResolverVisitRetriesCallbackFailure(t *testing.T) {
	root := t.TempDir()
	writeRootFSFile(t, filepath.Join(root, "file"), "content", 0644)
	resolver := NewSymLinkResolver(os.DirFS(root))
	failure := errors.New("visit failed")
	calls := 0
	callback := func(p Path, _ string, _ fs.FileInfo) error {
		if p.Abs() == "/file" {
			calls++
			if calls == 1 {
				return failure
			}
		}
		return nil
	}
	require.ErrorIs(t, resolver.Visit(NewPath("/file"), nil, callback), failure)
	require.NoError(t, resolver.Visit(NewPath("/file"), nil, callback))
	require.Equal(t, 2, calls)
}

func TestSymLinkResolverVisitFollowsCanonicalPath(t *testing.T) {
	root := t.TempDir()
	writeRootFSFile(t, filepath.Join(root, "usr/bin/tool"), "tool", 0755)
	require.NoError(t, os.Symlink("usr/bin", filepath.Join(root, "bin")))
	resolver := NewSymLinkResolver(os.DirFS(root))
	for range 2 {
		var visited []string
		var links []string
		err := resolver.Visit(NewPath("/bin/tool"), nil, func(p Path, link string, info fs.FileInfo) error {
			visited = append(visited, p.Abs())
			if info.Mode()&fs.ModeSymlink != 0 {
				links = append(links, link)
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, []string{"/", "/bin", "/", "/usr", "/usr/bin", "/usr/bin/tool"}, visited)
		require.Equal(t, []string{"usr/bin"}, links)
	}
}

func TestSymLinkResolverVisitSkipsInitialPath(t *testing.T) {
	resolver := NewSymLinkResolver(os.DirFS(t.TempDir()))
	starts := 0
	err := resolver.Visit(NewPath("/missing"), func(p Path) bool {
		starts++
		require.Equal(t, "/missing", p.Abs())
		return false
	}, func(Path, string, fs.FileInfo) error {
		t.Fatal("a skipped path must not be visited")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, starts)
}
