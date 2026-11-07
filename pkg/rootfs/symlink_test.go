package rootfs

import (
	"bytes"
	"fmt"
	"io/fs"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestRealPathResolvesLinksBeforeParentTraversal(t *testing.T) {
	for _, target := range []string{"jump/../file", "/jump/../file"} {
		t.Run(target, func(t *testing.T) {
			fss := fstest.MapFS{
				"alias":      {Mode: fs.ModeSymlink | 0777, Data: []byte(target)},
				"jump":       {Mode: fs.ModeSymlink | 0777, Data: []byte("real/child")},
				"real/child": {Mode: fs.ModeDir | 0755},
				"real/file":  {Mode: 0644, Data: []byte("correct")},
				"file":       {Mode: 0644, Data: []byte("decoy")},
			}
			resolved, err := MustPath("alias").RealPath(fss)
			require.NoError(t, err)
			require.Equal(t, MustPath("real/file"), *resolved)
			var out bytes.Buffer
			a := NewArchiver(fss, &out)
			require.NoError(t, a.Copy(MustPath("alias")))
			require.NoError(t, a.Close())
			entries := readRootFSTar(t, out.Bytes())
			require.Equal(t, "correct", entries["real/file"].body)
			require.NotContains(t, entries, "file")
			require.Equal(t, target, entries["alias"].header.Linkname)
		})
	}
}

func TestSymlinkTargetDirectorySuffix(t *testing.T) {
	for _, prefix := range []string{"", "/"} {
		for _, target := range []struct {
			name, resolved string
			isDir          bool
		}{
			{name: "file", resolved: "file"},
			{name: "file-link", resolved: "file"},
			{name: "dir/child", resolved: "dir/child", isDir: true},
			{name: "dir-link", resolved: "dir/child", isDir: true},
		} {
			for _, suffix := range []struct {
				value  string
				parent bool
			}{
				{value: "/"},
				{value: "/."},
				{value: "/./"},
				{value: "/..", parent: true},
				{value: "/../", parent: true},
			} {
				link := prefix + target.name + suffix.value
				t.Run(link, func(t *testing.T) {
					fss := fstest.MapFS{
						"alias":     {Mode: fs.ModeSymlink | 0777, Data: []byte(link)},
						"file":      {Mode: 0644, Data: []byte("data")},
						"file-link": {Mode: fs.ModeSymlink | 0777, Data: []byte("file")},
						"dir/child": {Mode: fs.ModeDir | 0755},
						"dir-link":  {Mode: fs.ModeSymlink | 0777, Data: []byte("dir/child")},
					}
					resolved, resolveErr := MustPath("alias").RealPath(fss)
					var out bytes.Buffer
					a := NewArchiver(fss, &out)
					copyErr := a.Copy(MustPath("alias"))
					require.NoError(t, a.Close())
					if !target.isDir {
						require.ErrorContains(t, resolveErr, "non-directory")
						require.Nil(t, resolved)
						require.ErrorContains(t, copyErr, "non-directory")
						return
					}
					require.NoError(t, resolveErr)
					require.NoError(t, copyErr)
					want := MustPath(target.resolved)
					if suffix.parent {
						want = want.Dir()
					}
					require.Equal(t, want, *resolved)
					entries := readRootFSTar(t, out.Bytes())
					require.Contains(t, entries, want.TarName())
					require.Equal(t, link, entries["alias"].header.Linkname)
				})
			}
		}
	}
}

func TestRealPathSymlinkLimit(t *testing.T) {
	fss := fstest.MapFS{}
	for i := range 257 {
		fss[fmt.Sprintf("link%d", i)] = &fstest.MapFile{Mode: fs.ModeSymlink | 0777, Data: fmt.Appendf(nil, "link%d", i+1)}
	}
	fss["link257"] = &fstest.MapFile{Mode: 0644}
	resolver := NewSymlinkResolver(fss)
	_, err := resolver.Resolve(MustPath("link0"))
	require.ErrorContains(t, err, "too many symlinks")
	require.Zero(t, resolver.(*symlinkResolver).resolving.Size())
	resolved, err := resolver.Resolve(MustPath("link257"))
	require.NoError(t, err)
	require.Equal(t, MustPath("link257"), *resolved)
}

type lstatErrorFS struct {
	fs.FS
	cause error
}

func (f lstatErrorFS) Lstat(name string) (fs.FileInfo, error) {
	if name == "target" {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: f.cause}
	}
	return fs.Lstat(f.FS, name)
}

func (f lstatErrorFS) ReadLink(name string) (string, error) { return fs.ReadLink(f.FS, name) }

func TestArchiverCopyPropagatesResolutionErrors(t *testing.T) {
	for _, cause := range []error{fs.ErrNotExist, syscall.ENOTDIR, fs.ErrPermission, syscall.EIO} {
		t.Run(cause.Error(), func(t *testing.T) {
			fss := lstatErrorFS{FS: fstest.MapFS{
				"alias":  {Mode: fs.ModeSymlink | 0777, Data: []byte("target")},
				"target": {Mode: 0644},
			}, cause: cause}
			var out bytes.Buffer
			a := NewArchiver(fss, &out)
			require.ErrorIs(t, a.Copy(MustPath("alias")), cause)
			require.NoError(t, a.Close())
			if cause == fs.ErrNotExist {
				require.Contains(t, readRootFSTar(t, out.Bytes()), "alias")
			} else {
				require.NotContains(t, readRootFSTar(t, out.Bytes()), "alias")
			}
		})
	}
	t.Run("missing input", func(t *testing.T) {
		var out bytes.Buffer
		a := NewArchiver(fstest.MapFS{}, &out)
		require.ErrorIs(t, a.Copy(MustPath("absent")), fs.ErrNotExist)
		require.NoError(t, a.Close())
	})
}

func TestArchiverStopsBeforeSpecialTargets(t *testing.T) {
	for _, target := range []string{"/proc/self/fd/1", "/sys/class/net"} {
		t.Run(target, func(t *testing.T) {
			fss := fstest.MapFS{"alias": {Mode: fs.ModeSymlink | 0777, Data: []byte(target)}}
			var out bytes.Buffer
			a := NewArchiver(fss, &out)
			a.SetExcludes(NewPathSet(MustPaths("/proc", "/sys")))
			require.NoError(t, a.Copy(MustPath("alias")))
			require.NoError(t, a.Close())
			entries := readRootFSTar(t, out.Bytes())
			require.Len(t, entries, 2)
			require.Equal(t, target, entries["alias"].header.Linkname)
		})
	}
}
