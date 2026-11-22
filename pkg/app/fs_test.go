package app

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/require"
)

func TestOpenFsImageArchive(t *testing.T) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, dir := range []string{"var/", "var/lib/", "var/lib/dpkg/"} {
		require.NoError(t, w.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0755}))
	}
	const status = "Package: example\nArchitecture: all\nStatus: install ok installed\n"
	require.NoError(t, w.WriteHeader(&tar.Header{
		Name: "var/lib/dpkg/status", Mode: 0644, Size: int64(len(status)),
	}))
	_, err := w.Write([]byte(status))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	img, err := mutate.AppendLayers(empty.Image, layer)
	require.NoError(t, err)
	archive := filepath.Join(t.TempDir(), "image.tar")
	require.NoError(t, tarball.WriteToFile(archive, name.MustParseReference("example:test"), img))
	opener, err := openFs(archive)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opener.Close()) })
	data, err := fs.ReadFile(opener.FS(), "var/lib/dpkg/status")
	require.NoError(t, err)
	require.Equal(t, status, string(data))
}
