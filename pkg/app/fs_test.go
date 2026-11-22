package app

import (
	"archive/tar"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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

func TestCommandRootfsPrefixes(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "")
	root := t.TempDir()
	content := (elftest.Fixture{Static: true}).Bytes()
	for _, dir := range []string{"var/lib/dpkg", "usr/bin"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "var/lib/dpkg/status"), nil, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "usr/bin/app"), content, 0755))
	archive := writeRootfsTar(t, os.DirFS(root))
	layer, err := tarball.LayerFromFile(archive)
	require.NoError(t, err)
	img, err := mutate.AppendLayers(empty.Image, layer)
	require.NoError(t, err)
	imageArchive := filepath.Join(t.TempDir(), "image.tar")
	require.NoError(t, tarball.WriteToFile(imageArchive, name.MustParseReference("example:test"), img))
	for _, input := range []string{"tar:" + archive, "oci:" + imageArchive} {
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

func imageArchive(t *testing.T, layers ...[]byte) string {
	t.Helper()
	dir := t.TempDir()
	var imageLayers []v1.Layer
	for i, data := range layers {
		file, err := os.CreateTemp(dir, "layer-*.tar")
		require.NoError(t, err)
		_, err = file.Write(data)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		layer, err := tarball.LayerFromFile(file.Name())
		require.NoError(t, err, "layer %d", i)
		imageLayers = append(imageLayers, layer)
	}
	img, err := mutate.AppendLayers(empty.Image, imageLayers...)
	require.NoError(t, err)
	file := filepath.Join(dir, "image.tar")
	require.NoError(t, tarball.WriteToFile(file, name.MustParseReference("example:test"), img))
	return file
}

type layerEntry struct{ name, body, link string }

func layerTar(t *testing.T, entries ...layerEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0644, Size: int64(len(e.body))}
		if strings.HasSuffix(e.name, "/") {
			hdr.Typeflag, hdr.Mode = tar.TypeDir, 0755
		} else if e.link != "" {
			hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, e.link
		}
		require.NoError(t, w.WriteHeader(hdr))
		_, err := w.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestOpenFsImageLayers(t *testing.T) {
	base := layerTar(t,
		layerEntry{"keep", "base", ""}, layerEntry{"replace", "old", ""}, layerEntry{"remove", "old", ""},
		layerEntry{"opaque/", "", ""}, layerEntry{"opaque/old", "old", ""},
	)
	upper := layerTar(t,
		layerEntry{"replace", "new", ""}, layerEntry{".wh.remove", "", ""},
		layerEntry{"opaque/", "", ""}, layerEntry{"opaque/.wh..wh..opq", "", ""}, layerEntry{"opaque/new", "new", ""},
		layerEntry{"link", "", "keep"},
	)
	archive := imageArchive(t, base, upper)
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	handle, err := openFs("oci:" + archive)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Close() })
	for path, want := range map[string]string{"keep": "base", "replace": "new", "opaque/new": "new", "link": "base"} {
		data, err := fs.ReadFile(handle.FS(), path)
		require.NoError(t, err, path)
		require.Equal(t, want, string(data), path)
	}
	for _, path := range []string{"remove", "opaque/old", ".wh.remove", "opaque/.wh..wh..opq"} {
		_, err := fs.Stat(handle.FS(), path)
		require.ErrorIs(t, err, fs.ErrNotExist, path)
	}
	entries, err := fs.ReadDir(handle.FS(), "opaque")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "new", entries[0].Name())
	require.NoError(t, handle.Close())
	remaining, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	require.Empty(t, remaining, "closing the filesystem must remove its temporary tar")
	require.FileExists(t, archive)
}

func TestOpenFsErrors(t *testing.T) {
	dir := t.TempDir()
	malformed := filepath.Join(dir, "malformed.tar")
	require.NoError(t, os.WriteFile(malformed, []byte("not a tar archive"), 0644))
	for _, input := range []string{"rootfs.tar", "unknown:rootfs.tar", "dir:/", "tar:" + filepath.Join(dir, "missing"), "tar:" + malformed, "oci:" + malformed} {
		t.Run(input, func(t *testing.T) {
			handle, err := openFs(input)
			require.Error(t, err)
			require.Nil(t, handle)
		})
	}
	require.FileExists(t, malformed, "failed reads must preserve the input archive")
}

func TestOpenFsImageExtractionFailureCleansUp(t *testing.T) {
	archive := imageArchive(t, []byte("not a tar archive"))
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	handle, err := openFs("oci:" + archive)
	require.Error(t, err)
	require.Nil(t, handle)
	remaining, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	require.Empty(t, remaining, "failed extraction must remove its temporary tar")
	require.FileExists(t, archive)
}

func TestOpenFsTemporaryDirectoryMissing(t *testing.T) {
	archive := imageArchive(t, layerTar(t))
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	handle, err := openFs("oci:" + archive)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.Nil(t, handle)
}

func TestOpenFsDefaultsToHostRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hello")
	require.NoError(t, os.WriteFile(file, []byte("host file"), 0644))
	handle, err := openFs("")
	require.NoError(t, err)
	defer handle.Close()
	data, err := fs.ReadFile(handle.FS(), strings.TrimPrefix(filepath.ToSlash(file), "/"))
	require.NoError(t, err)
	require.Equal(t, "host file", string(data))
}

func TestCommandsRejectUnknownRootfs(t *testing.T) {
	for _, command := range []string{"scan", "bundle"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			out := filepath.Join(t.TempDir(), "out.tar")
			args := []string{"bistro", command, "--rootfs", "unknown:rootfs"}
			if command == "bundle" {
				args = append(args, "--out", out)
			}
			args = append(args, "/usr/bin/env")
			err := New(NewInput{Cwd: "/", Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), args)
			require.EqualError(t, err, "unsupported input format: unknown:rootfs")
			require.Empty(t, stdout.String())
			require.Empty(t, stderr.String())
			require.NoFileExists(t, out)
		})
	}
}

func TestScanRejectsUnknownFormat(t *testing.T) {
	fixture := newAppFixture(t)
	err := fixture.run("scan", "--format", "unknown", "/missing")
	require.EqualError(t, err, "unknown format: unknown")
	require.Empty(t, fixture.stdout.String())
	require.Empty(t, fixture.stderr.String())
}

func writeRootfsTar(t *testing.T, fss fs.FS) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "rootfs.tar")
	f, err := os.Create(archive)
	require.NoError(t, err)
	w := tar.NewWriter(f)
	err = fs.WalkDir(fss, ".", func(path string, e fs.DirEntry, err error) error {
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
			data, err := fs.ReadFile(fss, path)
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
	return archive
}
