package app

import (
	"archive/tar"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
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
	opener, err := openFs("oci:" + archive)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opener.Close()) })
	data, err := fs.ReadFile(opener.FS(), "var/lib/dpkg/status")
	require.NoError(t, err)
	require.Equal(t, status, string(data))
}

func TestOpenFsRelativeDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("root", 0755))
	require.NoError(t, os.WriteFile("root/hello", []byte("hello"), 0644))
	opener, err := openFs("dir:root")
	require.NoError(t, err)
	defer opener.Close()
	data, err := fs.ReadFile(opener.FS(), "hello")
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
}

func TestCommandRootfsPrefixes(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "")
	root := t.TempDir()
	content := (elftest.Fixture{Static: true}).Bytes()
	for _, dir := range []string{"var/lib/dpkg", "usr/bin"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "var/lib/dpkg/status"), nil, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "usr/bin/app"), content, 0755))
	archive := filepath.Join(t.TempDir(), "rootfs.tar")
	f, err := os.Create(archive)
	require.NoError(t, err)
	w := tar.NewWriter(f)
	err = fs.WalkDir(os.DirFS(root), ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = path
		if err := w.WriteHeader(hdr); err != nil {
			return err
		}
		if !e.IsDir() {
			data, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				return err
			}
			_, err = w.Write(data)
			return err
		}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())
	layer, err := tarball.LayerFromFile(archive)
	require.NoError(t, err)
	img, err := mutate.AppendLayers(empty.Image, layer)
	require.NoError(t, err)
	imageArchive := filepath.Join(t.TempDir(), "image.tar")
	require.NoError(t, tarball.WriteToFile(imageArchive, name.MustParseReference("example:test"), img))
	for _, input := range []string{"dir:" + root, "tar:" + archive, "oci:" + imageArchive} {
		for _, command := range []string{"scan", "bundle"} {
			t.Run(command+"/"+input[:3], func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				app := New(NewInput{Cwd: "/", Stdout: &stdout, Stderr: &stderr})
				out := filepath.Join(t.TempDir(), "out.tar")
				args := []string{"bistro", command, "--rootfs", input}
				if command == "bundle" {
					args = append(args, "--out", out)
				}
				args = append(args, "/usr/bin/app")
				require.NoError(t, app.Run(context.Background(), args))
				require.Empty(t, stderr.String())
				if command == "scan" {
					require.Equal(t, "/usr/bin/app\n", stdout.String())
				} else {
					require.Equal(t, map[string]string{"usr/bin/app": string(content)}, readArchiveFiles(t, out))
				}
				require.FileExists(t, archive)
				require.FileExists(t, imageArchive)
			})
		}
	}
}
