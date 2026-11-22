package tarfs_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/tarfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type entry struct {
	name, body, link string
	kind             byte
	mode             int64
}

func newArchive(t *testing.T, entries ...entry) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		kind, mode := e.kind, int64(0644)
		if kind == 0 {
			kind = tar.TypeReg
		}
		if kind == tar.TypeDir {
			mode = 0755
		}
		if e.mode != 0 {
			mode = e.mode
		}
		require.NoError(t, w.WriteHeader(&tar.Header{
			Name: e.name, Typeflag: kind, Mode: mode,
			Size: int64(len(e.body)), Linkname: e.link,
		}))
		_, err := io.WriteString(w, e.body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return bytes.NewReader(buf.Bytes())
}

func newFS(t *testing.T, entries ...entry) fs.FS {
	t.Helper()
	fss, err := tarfs.New(newArchive(t, entries...))
	require.NoError(t, err)
	return fss
}

func TestReadFileAndReadAt(t *testing.T) {
	fss := newFS(t, entry{name: "first", body: "first content"}, entry{name: "second", body: "second content"})
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(fss, name)
			require.NoError(t, err)
			assert.Equal(t, name+" content", string(data))
			f, err := fss.Open(name)
			require.NoError(t, err)
			defer f.Close()
			ra, ok := f.(io.ReaderAt)
			require.True(t, ok, "ELF parsing requires io.ReaderAt")
			buf := make([]byte, 7)
			n, err := ra.ReadAt(buf, int64(len(name)+1))
			require.NoError(t, err)
			assert.Equal(t, 7, n)
			assert.Equal(t, "content", string(buf))
			n, err = ra.ReadAt(buf, int64(len(data)-2))
			require.ErrorIs(t, err, io.EOF)
			assert.Equal(t, 2, n)
			assert.Equal(t, "nt", string(buf[:n]))
			n, err = ra.ReadAt(buf, int64(len(data)))
			require.ErrorIs(t, err, io.EOF)
			assert.Zero(t, n, "reads must not reach the next tar header")
			data, err = io.ReadAll(f)
			require.NoError(t, err)
			assert.Equal(t, name+" content", string(data), "ReadAt must preserve the sequential offset")
			info, err := f.Stat()
			require.NoError(t, err)
			assert.Equal(t, int64(len(data)), info.Size())
			assert.Equal(t, fs.FileMode(0644), info.Mode())
		})
	}
}

func TestReadDir(t *testing.T) {
	for _, prefix := range []string{"", "./"} {
		for _, explicitRoot := range []bool{false, true} {
			t.Run(prefix+"/root="+map[bool]string{false: "implicit", true: "explicit"}[explicitRoot], func(t *testing.T) {
				entries := []entry{
					{name: prefix + "usr/share/z.txt", body: "z"},
					{name: prefix + "usr/share/sub/file", body: "nested"},
					{name: prefix + "usr/share/sub/", kind: tar.TypeDir},
					{name: prefix + "usr/share/a.txt", body: "a"},
					{name: prefix + "usr/share/", kind: tar.TypeDir},
					{name: prefix + "usr/", kind: tar.TypeDir},
					{name: prefix + "empty/", kind: tar.TypeDir},
					{name: prefix + "alias", kind: tar.TypeSymlink, link: "usr/share"},
				}
				if explicitRoot {
					rootName := "."
					if prefix != "" {
						rootName = prefix
					}
					entries = append(entries, entry{name: rootName, kind: tar.TypeDir, mode: 0700})
				}
				fss := newFS(t, entries...)
				root, err := fs.Stat(fss, ".")
				require.NoError(t, err)
				if explicitRoot {
					assert.Equal(t, fs.FileMode(0700), root.Mode().Perm())
				} else {
					assert.Equal(t, fs.FileMode(0755), root.Mode().Perm())
				}
				for _, tc := range []struct {
					path string
					want []string
				}{
					{".", []string{"alias", "empty", "usr"}},
					{"usr", []string{"share"}},
					{"usr/share", []string{"a.txt", "sub", "z.txt"}},
					{"usr/share/sub", []string{"file"}},
					{"alias", []string{"a.txt", "sub", "z.txt"}},
					{"empty", []string{}},
				} {
					got, err := fs.ReadDir(fss, tc.path)
					require.NoError(t, err, tc.path)
					names := make([]string, 0, len(got))
					for _, e := range got {
						names = append(names, e.Name())
						info, err := e.Info()
						require.NoError(t, err)
						assert.Equal(t, info.IsDir(), e.IsDir())
						assert.Equal(t, info.Mode().Type(), e.Type())
					}
					assert.Equal(t, tc.want, names, tc.path)
				}
				var files []string
				err = fs.WalkDir(fss, ".", func(name string, e fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if e.Type().IsRegular() {
						files = append(files, name)
					}
					return nil
				})
				require.NoError(t, err)
				assert.Equal(t, []string{"usr/share/a.txt", "usr/share/sub/file", "usr/share/z.txt"}, files)
			})
		}
	}
}

func TestSymlinks(t *testing.T) {
	fss := newFS(t,
		entry{name: "dir/", kind: tar.TypeDir},
		entry{name: "dir/file", body: "target"},
		entry{name: "dir/relative", kind: tar.TypeSymlink, link: "file"},
		entry{name: "absolute", kind: tar.TypeSymlink, link: "/dir/file"},
		entry{name: "alias", kind: tar.TypeSymlink, link: "dir"},
		entry{name: "dangling", kind: tar.TypeSymlink, link: "missing"},
		entry{name: "loop", kind: tar.TypeSymlink, link: "loop"},
	)
	for _, name := range []string{"dir/relative", "absolute", "alias/file"} {
		data, err := fs.ReadFile(fss, name)
		require.NoError(t, err, name)
		assert.Equal(t, "target", string(data))
		info, err := fs.Stat(fss, name)
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular())
	}
	link, err := fs.ReadLink(fss, "dir/relative")
	require.NoError(t, err)
	assert.Equal(t, "file", link)
	info, err := fs.Lstat(fss, "dir/relative")
	require.NoError(t, err)
	assert.Equal(t, fs.ModeSymlink, info.Mode().Type())
	_, err = fs.ReadFile(fss, "dangling")
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = fs.ReadFile(fss, "loop")
	require.ErrorContains(t, err, "circular symlink")
}

func TestErrors(t *testing.T) {
	fss := newFS(t, entry{name: "file", body: "content"})
	for _, read := range []func(string) error{
		func(name string) error { _, err := fss.Open(name); return err },
		func(name string) error { _, err := fs.ReadFile(fss, name); return err },
		func(name string) error { _, err := fs.Stat(fss, name); return err },
		func(name string) error { _, err := fs.Lstat(fss, name); return err },
		func(name string) error { _, err := fs.ReadDir(fss, name); return err },
		func(name string) error { _, err := fs.ReadLink(fss, name); return err },
	} {
		require.ErrorIs(t, read("missing"), fs.ErrNotExist)
		require.ErrorContains(t, read("../escape"), "path cannot start with '..'")
	}
	_, err := fs.ReadDir(fss, "file")
	require.ErrorIs(t, err, fs.ErrInvalid)
	_, err = fs.ReadLink(fss, "file")
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func TestNewRejectsParentTraversal(t *testing.T) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "../escape", Mode: 0644}))
	require.NoError(t, w.Close())
	fss, err := tarfs.New(bytes.NewReader(buf.Bytes()))
	require.ErrorContains(t, err, "path cannot start with '..'")
	require.Nil(t, fss)
}

type failingSeeker struct {
	*bytes.Reader
	err error
}

func (r failingSeeker) Seek(int64, int) (int64, error) {
	return 0, r.err
}

func TestNewPropagatesSeekError(t *testing.T) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "file", Mode: 0644}))
	require.NoError(t, w.Close())
	failure := errors.New("seek failed")
	fss, err := tarfs.New(failingSeeker{Reader: bytes.NewReader(buf.Bytes()), err: failure})
	require.ErrorIs(t, err, failure)
	require.Nil(t, fss)
}

func TestNewRejectsMalformedArchive(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "invalid-*.tar")
	require.NoError(t, err)
	defer f.Close()
	_, err = io.Copy(f, strings.NewReader("invalid tar archive"))
	require.NoError(t, err)
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	_, err = tarfs.New(f)
	require.Error(t, err)
}

func TestHardLinks(t *testing.T) {
	target := entry{name: "data/original", body: "payload", mode: 0751}
	alias := entry{name: "bin/alias", kind: tar.TypeLink, link: "data/original"}
	for _, tc := range []struct {
		name    string
		entries []entry
	}{
		{"backward", []entry{target, alias}},
		{"forward", []entry{alias, target}},
		{"chain", []entry{
			{name: "bin/alias", kind: tar.TypeLink, link: "data/second"},
			{name: "data/second", kind: tar.TypeLink, link: "data/original"},
			target,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fss := newFS(t, tc.entries...)
			for _, path := range []string{"bin/alias", "data/original"} {
				data, err := fs.ReadFile(fss, path)
				require.NoError(t, err)
				assert.Equal(t, "payload", string(data))
			}
			info, err := fs.Stat(fss, "bin/alias")
			require.NoError(t, err)
			assert.Equal(t, "alias", info.Name())
			assert.Equal(t, int64(7), info.Size())
			assert.Equal(t, fs.FileMode(0751), info.Mode())
			info, err = fs.Lstat(fss, "data/original")
			require.NoError(t, err)
			assert.Equal(t, "original", info.Name(), "resolving an alias must preserve the target header")
			entries, err := fs.ReadDir(fss, "bin")
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, "alias", entries[0].Name())
			assert.True(t, entries[0].Type().IsRegular())
			info, err = entries[0].Info()
			require.NoError(t, err)
			assert.Equal(t, int64(7), info.Size())
			assert.Equal(t, fs.FileMode(0751), info.Mode())
			f, err := fss.Open("bin/alias")
			require.NoError(t, err)
			defer f.Close()
			ra, ok := f.(io.ReaderAt)
			require.True(t, ok)
			buf := make([]byte, 3)
			n, err := ra.ReadAt(buf, 2)
			require.NoError(t, err)
			assert.Equal(t, 3, n)
			assert.Equal(t, "ylo", string(buf))
		})
	}
}

func TestOverwrittenHardLinks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		oldTarget   string
		replacement entry
		wantType    fs.FileMode
	}{
		{"regular file", "old-target", entry{name: "alias", body: "new", mode: 0751}, 0},
		{"normalized path", "old-target", entry{name: "./alias", body: "new", mode: 0751}, 0},
		{"dangling link", "missing", entry{name: "alias", body: "new", mode: 0751}, 0},
		{"self link", "alias", entry{name: "alias", body: "new", mode: 0751}, 0},
		{"symlink", "old-target", entry{name: "alias", kind: tar.TypeSymlink, link: "new-target"}, fs.ModeSymlink},
		{"hard link", "old-target", entry{name: "alias", kind: tar.TypeLink, link: "new-target"}, 0},
		{"dangling link replaced by hard link", "missing", entry{name: "alias", kind: tar.TypeLink, link: "new-target"}, 0},
		{"directory", "old-target", entry{name: "alias/", kind: tar.TypeDir, mode: 0700}, fs.ModeDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fss := newFS(t,
				entry{name: "old-target", body: "old"},
				entry{name: "alias", kind: tar.TypeLink, link: tc.oldTarget},
				tc.replacement,
				entry{name: "new-target", body: "new", mode: 0751},
			)
			info, err := fs.Lstat(fss, "alias")
			require.NoError(t, err)
			assert.Equal(t, "alias", info.Name())
			assert.Equal(t, tc.wantType, info.Mode().Type())
			if tc.wantType == fs.ModeDir {
				assert.Equal(t, fs.FileMode(0700), info.Mode().Perm())
				entries, err := fs.ReadDir(fss, "alias")
				require.NoError(t, err)
				assert.Empty(t, entries)
			} else {
				data, err := fs.ReadFile(fss, "alias")
				require.NoError(t, err)
				assert.Equal(t, "new", string(data))
				if tc.wantType == fs.ModeSymlink {
					link, err := fs.ReadLink(fss, "alias")
					require.NoError(t, err)
					assert.Equal(t, "new-target", link)
				} else {
					assert.Equal(t, fs.FileMode(0751), info.Mode().Perm())
					assert.Equal(t, int64(3), info.Size())
				}
			}
			entries, err := fs.ReadDir(fss, ".")
			require.NoError(t, err)
			require.Len(t, entries, 3)
			assert.Equal(t, "alias", entries[0].Name())
			assert.Equal(t, tc.wantType, entries[0].Type())
			data, err := fs.ReadFile(fss, "old-target")
			require.NoError(t, err)
			assert.Equal(t, "old", string(data))
		})
	}
}

func TestHardLinkToSymlink(t *testing.T) {
	fss := newFS(t,
		entry{name: "b/alias", kind: tar.TypeLink, link: "a/link"},
		entry{name: "a/link", kind: tar.TypeSymlink, link: "target"},
		entry{name: "a/target", body: "original directory"},
		entry{name: "b/target", body: "alias directory"},
	)
	link, err := fs.ReadLink(fss, "b/alias")
	require.NoError(t, err)
	assert.Equal(t, "target", link)
	info, err := fs.Lstat(fss, "b/alias")
	require.NoError(t, err)
	assert.Equal(t, "alias", info.Name())
	assert.Equal(t, fs.ModeSymlink, info.Mode().Type())
	data, err := fs.ReadFile(fss, "b/alias")
	require.NoError(t, err)
	assert.Equal(t, "alias directory", string(data), "relative symlinks resolve from the alias directory")
	data, err = fs.ReadFile(fss, "a/link")
	require.NoError(t, err)
	assert.Equal(t, "original directory", string(data))
	entries, err := fs.ReadDir(fss, "b")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "alias", entries[0].Name())
	assert.Equal(t, fs.ModeSymlink, entries[0].Type())
}

func TestHardLinkToDanglingSymlink(t *testing.T) {
	fss := newFS(t,
		entry{name: "alias", kind: tar.TypeLink, link: "symlink"},
		entry{name: "symlink", kind: tar.TypeSymlink, link: "missing"},
	)
	link, err := fs.ReadLink(fss, "alias")
	require.NoError(t, err)
	assert.Equal(t, "missing", link)
	_, err = fs.ReadFile(fss, "alias")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestNewRejectsInvalidHardLinks(t *testing.T) {
	var longChain []entry
	for i := 0; i < 300; i++ {
		longChain = append(longChain, entry{
			name: fmt.Sprintf("link%d", i), kind: tar.TypeLink, link: fmt.Sprintf("link%d", i+1),
		})
	}
	longChain = append(longChain, entry{name: "link300", body: "payload"})
	for _, tc := range []struct {
		name    string
		entries []entry
		want    error
	}{
		{"missing", []entry{{name: "alias", kind: tar.TypeLink, link: "missing"}}, fs.ErrNotExist},
		{"self", []entry{{name: "alias", kind: tar.TypeLink, link: "alias"}}, fs.ErrInvalid},
		{"cycle", []entry{
			{name: "alias", kind: tar.TypeLink, link: "second"},
			{name: "second", kind: tar.TypeLink, link: "alias"},
		}, fs.ErrInvalid},
		{"too many links", longChain, fs.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fss, err := tarfs.New(newArchive(t, tc.entries...))
			require.ErrorIs(t, err, tc.want)
			require.Nil(t, fss)
			var pathErr *fs.PathError
			require.ErrorAs(t, err, &pathErr)
			assert.Equal(t, "link", pathErr.Op)
			assert.NotEmpty(t, pathErr.Path)
		})
	}
}

func TestImplicitParentDirectories(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []entry
		usrMode fs.FileMode
	}{
		{"no directory headers", []entry{{name: "usr/bin/app", body: "payload"}}, 0755},
		{"explicit root", []entry{
			{name: ".", kind: tar.TypeDir},
			{name: "usr/bin/app", body: "payload"},
		}, 0755},
		{"explicit parent before child", []entry{
			{name: "usr/", kind: tar.TypeDir, mode: 0700},
			{name: "usr/bin/app", body: "payload"},
		}, 0700},
		{"explicit parent after child", []entry{
			{name: "usr/bin/app", body: "payload"},
			{name: "usr/", kind: tar.TypeDir, mode: 0700},
		}, 0700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fss := newFS(t, tc.entries...)
			data, err := fs.ReadFile(fss, "usr/bin/app")
			require.NoError(t, err)
			assert.Equal(t, "payload", string(data))
			for _, path := range []string{".", "usr", "usr/bin"} {
				info, err := fs.Stat(fss, path)
				require.NoError(t, err, path)
				assert.True(t, info.IsDir(), path)
			}
			info, err := fs.Stat(fss, "usr")
			require.NoError(t, err)
			assert.Equal(t, tc.usrMode, info.Mode().Perm())
			var paths []string
			err = fs.WalkDir(fss, ".", func(path string, _ fs.DirEntry, err error) error {
				if err == nil {
					paths = append(paths, path)
				}
				return err
			})
			require.NoError(t, err)
			assert.Equal(t, []string{".", "usr", "usr/bin", "usr/bin/app"}, paths)
		})
	}
}

func TestEmptyArchiveHasRoot(t *testing.T) {
	fss := newFS(t)
	info, err := fs.Stat(fss, ".")
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, fs.FileMode(0755), info.Mode().Perm())
	entries, err := fs.ReadDir(fss, ".")
	require.NoError(t, err)
	assert.Empty(t, entries)
}
