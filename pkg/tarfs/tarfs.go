package tarfs

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"slices"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Reader interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

type iTarFS interface {
	fs.FS
	fs.ReadFileFS
	fs.ReadLinkFS
	fs.ReadDirFS
	fs.StatFS
}

type tarFS struct {
	files      map[rootfs.Path]*tarEntry
	dirEntries map[rootfs.Path][]fs.DirEntry
	tarFile    Reader
}

var _ iTarFS = (*tarFS)(nil)
var _ fs.File = (*tarFile)(nil)

func (t *tarFS) Open(name string) (fs.File, error) {
	return t.open(name)
}

func (t *tarFS) open(name string) (*tarFile, error) {
	f, err := t.resolveFile(name)
	if err != nil {
		return nil, err
	}
	reader := io.NewSectionReader(t.tarFile, f.offset, f.hdr.Size)
	tf := &tarFile{tarEntry: f, reader: reader}
	return tf, nil
}

func (t *tarFS) ReadFile(name string) ([]byte, error) {
	f, err := t.open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (t *tarFS) ReadLink(name string) (string, error) {
	f, err := t.getFile(name)
	if err != nil {
		return "", err
	}
	if f.hdr.Typeflag == tar.TypeSymlink {
		return f.hdr.Linkname, nil
	}
	return "", fmt.Errorf("[tarfs] %s: %w", name, fs.ErrInvalid)
}

func (t *tarFS) Stat(name string) (fs.FileInfo, error) {
	f, err := t.resolveFile(name)
	if err != nil {
		return nil, err
	}
	return f.hdr.FileInfo(), nil
}

func (t *tarFS) Lstat(name string) (fs.FileInfo, error) {
	f, err := t.getFile(name)
	if err != nil {
		return nil, err
	}
	return f.hdr.FileInfo(), nil
}

func (t *tarFS) ReadDir(name string) ([]fs.DirEntry, error) {
	dir, err := t.resolveFile(name)
	if err != nil {
		return nil, err
	}
	if dir.hdr.Typeflag != tar.TypeDir {
		return nil, fmt.Errorf("[tarfs] %s: %w", name, fs.ErrInvalid)
	}
	return t.dirEntries[dir.path], nil
}

func (t *tarFS) getFile(name string) (*tarEntry, error) {
	p, err := rootfs.NewPath(name)
	if err != nil {
		return nil, err
	}
	if f, ok := t.files[p]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("[tarfs] %s: %w", name, fs.ErrNotExist)
}

func (t *tarFS) resolveFile(name string) (*tarEntry, error) {
	path, err := rootfs.NewPath(name)
	if err != nil {
		return nil, err
	}
	realpath, err := path.RealPath(t)
	if err != nil {
		return nil, err
	}
	return t.getFile(realpath.TarName())
}

func New(f Reader) (fs.FS, error) {
	tr := tar.NewReader(f)
	var files = make(map[rootfs.Path]*tarEntry)
	var hasRoot bool
	var result = &tarFS{tarFile: f}
	var dirEntries = make(map[rootfs.Path][]fs.DirEntry)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		offset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		path, err := rootfs.NewPath(hdr.Name)
		if err != nil {
			return nil, err
		}
		file := &tarEntry{offset: offset, path: path, hdr: hdr}
		files[path] = file
		if path.Abs() != "/" {
			dir := path.Dir()
			dirEntries[dir] = append(dirEntries[dir], file)
		}
		if path.Abs() == "/" {
			hasRoot = true
		}
	}
	if !hasRoot {
		hdr := &tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0755}
		path := rootfs.MustPath("/")
		files[path] = &tarEntry{offset: 0, path: path, hdr: hdr}
	}
	for _, entries := range dirEntries {
		slices.SortFunc(entries, func(a, b fs.DirEntry) int {
			if a.Name() < b.Name() {
				return -1
			}
			if a.Name() > b.Name() {
				return 1
			}
			return 0
		})
	}
	result.files = files
	result.dirEntries = dirEntries
	return result, nil
}
