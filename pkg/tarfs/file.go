package tarfs

import (
	"archive/tar"
	"io"
	"io/fs"
	"path"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type tarEntry struct {
	offset int64
	path   rootfs.Path
	hdr    *tar.Header
}

var _ fs.DirEntry = (*tarEntry)(nil)

func (t *tarEntry) Name() string {
	return path.Base(t.hdr.Name)
}

func (t *tarEntry) IsDir() bool {
	return t.hdr.Typeflag == tar.TypeDir
}

func (t *tarEntry) Type() fs.FileMode {
	return t.hdr.FileInfo().Mode().Type()
}

func (t *tarEntry) Info() (fs.FileInfo, error) {
	return t.hdr.FileInfo(), nil
}

type tarFile struct {
	*tarEntry
	reader *io.SectionReader
}

var _ fs.File = (*tarFile)(nil)

func (t *tarFile) Close() error {
	return nil
}

func (t *tarFile) Read(p []byte) (int, error) {
	return t.reader.Read(p)
}

func (t *tarFile) ReadAt(p []byte, off int64) (int, error) {
	return t.reader.ReadAt(p, off)
}

func (t *tarFile) Stat() (fs.FileInfo, error) {
	return t.hdr.FileInfo(), nil
}
