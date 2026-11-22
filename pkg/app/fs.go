package app

import (
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/loilo-inc/bistro/pkg/tarfs"
)

type FsOpener interface {
	FS() fs.FS
	Close() error
}

type dirFsOpener struct {
	fs fs.FS
}

func (d *dirFsOpener) FS() fs.FS {
	return d.fs
}

func (d *dirFsOpener) Close() error {
	return nil
}

type tarFsOpener struct {
	fs   fs.FS
	file *os.File
}

func (t *tarFsOpener) FS() fs.FS {
	return t.fs
}

func (t *tarFsOpener) Close() error {
	if t.file != nil {
		t.file.Close()
		return os.Remove(t.file.Name())
	}
	return nil
}

func openFs(tarfile string) (FsOpener, error) {
	if tarfile != "" {
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
		return &tarFsOpener{fs: tarFs, file: tmpFile}, nil
	}
	return &dirFsOpener{fs: os.DirFS("/")}, nil
}
