package rootfs_test

import (
	"errors"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

func TestFsReaderAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  int64
		size int
		want string
		err  error
	}{
		{name: "offset", off: 4, size: 3, want: "456"},
		{name: "partial read", off: 8, size: 4, want: "89", err: io.EOF},
		{name: "end of file", off: 10, size: 1, err: io.EOF},
		{name: "invalid offset", off: -1, size: 1, err: fs.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := (fstest.MapFS{"file": {Data: []byte("0123456789")}}).Open("file")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, file.Close()) })

			prefix := make([]byte, 2)
			_, err = io.ReadFull(file, prefix)
			require.NoError(t, err)

			reader := &rootfs.FsReaderAt{File: file}
			buf := make([]byte, tc.size)
			n, err := reader.ReadAt(buf, tc.off)
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, len(tc.want), n)
			require.Equal(t, tc.want, string(buf[:n]))

			// ReadAt must leave the sequential read position unchanged.
			n, err = file.Read(prefix)
			require.NoError(t, err)
			require.Equal(t, 2, n)
			require.Equal(t, "23", string(prefix))
		})
	}
}

type failingReaderAtFile struct {
	fs.File
	err error
}

func (f failingReaderAtFile) ReadAt(p []byte, off int64) (int, error) {
	return copy(p, "ab"), f.err
}

func TestFsReaderAtPropagatesReadError(t *testing.T) {
	want := errors.New("read failed")
	reader := &rootfs.FsReaderAt{File: failingReaderAtFile{err: want}}
	buf := make([]byte, 4)
	n, err := reader.ReadAt(buf, 0)
	require.ErrorIs(t, err, want)
	require.Equal(t, 2, n)
	require.Equal(t, "ab", string(buf[:n]))
}

func TestFsReaderAtRequiresReaderAt(t *testing.T) {
	file, err := (fstest.MapFS{"file": {Data: []byte("data")}}).Open("file")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	// Expose only fs.File, hiding the underlying file's ReadAt method.
	reader := &rootfs.FsReaderAt{File: struct{ fs.File }{file}}
	n, err := reader.ReadAt(make([]byte, 4), 0)
	require.ErrorContains(t, err, "client fs must implement io.ReaderAt")
	require.Zero(t, n)
}
