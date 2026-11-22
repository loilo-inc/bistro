package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/loilo-inc/bistro/pkg/tarfs"
)

type FsHandle interface {
	FS() fs.FS
	Close() error
}

type hostFsHandle struct {
	fs fs.FS
}

func (d *hostFsHandle) FS() fs.FS {
	return d.fs
}

func (d *hostFsHandle) Close() error {
	return nil
}

type tarFsOpener struct {
	fs         fs.FS
	file       *os.File
	removeFile bool
}

func (t *tarFsOpener) FS() fs.FS {
	return t.fs
}

func (t *tarFsOpener) Close() error {
	if t.file != nil {
		t.file.Close()
		if t.removeFile {
			os.Remove(t.file.Name())
		}
	}
	return nil
}

// openFs opens a filesystem based on the input string.
// Supported formats:
// - "" for the host filesystem
// - "tar:/path/to/tarfile" for tar archives
// - "oci:/path/to/oci/image" for OCI image layouts (actually tar archives)
func openFs(input string) (FsHandle, error) {
	if input == "" {
		return &hostFsHandle{fs: os.DirFS("/")}, nil
	}
	// Pure tar archive
	if tarPath, ok := strings.CutPrefix(input, "tar:"); ok {
		file, err := os.Open(tarPath)
		if err != nil {
			return nil, err
		}
		cleanup := func() {
			file.Close()
		}
		tarFs, err := tarfs.New(file)
		if err != nil {
			cleanup()
			return nil, err
		}
		return &tarFsOpener{fs: tarFs, file: file}, nil
	}
	// OCI Image Layout
	if tarfile, ok := strings.CutPrefix(input, "oci:"); ok {
		img, err := tarball.ImageFromPath(tarfile, nil)
		if err != nil {
			return nil, err
		}
		stream := mutate.Extract(img)
		defer stream.Close()
		tmpFile, err := os.CreateTemp("", path.Base(tarfile))
		if err != nil {
			return nil, err
		}
		cleanup := func() {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}
		_, err = io.Copy(tmpFile, stream)
		if err != nil {
			cleanup()
			return nil, err
		}
		if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
			cleanup()
			return nil, err
		}
		tarFs, err := tarfs.New(tmpFile)
		if err != nil {
			cleanup()
			return nil, err
		}
		return &tarFsOpener{fs: tarFs, file: tmpFile, removeFile: true}, nil
	}
	return nil, fmt.Errorf("unsupported input format: %s", input)
}
