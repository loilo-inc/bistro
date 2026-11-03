package util_test

import (
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/util"
	"github.com/stretchr/testify/require"
)

func TestReaderAtPreservesSequentialPosition(t *testing.T) {
	file, err := (fstest.MapFS{"file": {Data: []byte("abcdef")}}).Open("file")
	require.NoError(t, err)
	defer file.Close()
	reader := util.NewReaderAt(file)
	for _, tc := range []struct {
		name    string
		offset  int64
		size    int
		want    string
		wantErr error
	}{
		{"middle", 2, 3, "cde", nil},
		{"backward", 0, 2, "ab", nil},
		{"partial read", 4, 4, "ef", io.EOF},
		{"end of file", 6, 1, "", io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, tc.size)
			n, err := reader.ReadAt(buf, tc.offset)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.want, string(buf[:n]))
		})
	}
	remaining, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(remaining), "random reads must not consume sequential input")
}

func TestReaderAtRejectsFileWithoutRandomAccess(t *testing.T) {
	file, err := (fstest.MapFS{"file": {Data: []byte("abcdef")}}).Open("file")
	require.NoError(t, err)
	defer file.Close()
	// Expose only fs.File, as permitted by fs.FS.Open.
	sequential := struct{ fs.File }{file}
	buf := []byte("unchanged")
	n, err := util.NewReaderAt(sequential).ReadAt(buf, 2)
	require.ErrorIs(t, err, fs.ErrInvalid)
	require.Zero(t, n)
	require.Equal(t, "unchanged", string(buf))
	remaining, err := io.ReadAll(sequential)
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(remaining))
}
