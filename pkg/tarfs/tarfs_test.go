package tarfs_test

import (
	"archive/tar"
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
}

func newFS(t *testing.T, entries ...entry) fs.FS {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "archive-*.tar")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	w := tar.NewWriter(f)
	for _, e := range entries {
		kind, mode := e.kind, int64(0644)
		if kind == 0 {
			kind = tar.TypeReg
		}
		if kind == tar.TypeDir {
			mode = 0755
		}
		require.NoError(t, w.WriteHeader(&tar.Header{
			Name: e.name, Typeflag: kind, Mode: mode,
			Size: int64(len(e.body)), Linkname: e.link,
		}))
		_, err := io.WriteString(w, e.body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	fss, err := tarfs.New(f)
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
					entries = append(entries, entry{name: ".", kind: tar.TypeDir})
				}
				fss := newFS(t, entries...)
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
				err := fs.WalkDir(fss, ".", func(name string, e fs.DirEntry, err error) error {
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
	}
	_, err := fs.ReadDir(fss, "file")
	require.ErrorIs(t, err, fs.ErrInvalid)
	_, err = fs.ReadLink(fss, "file")
	require.ErrorIs(t, err, fs.ErrInvalid)
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
