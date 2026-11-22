package tarfs

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type TarFS interface {
	fs.FS
	fs.ReadFileFS
	fs.ReadLinkFS
	fs.ReadDirFS
	fs.StatFS
}

type tarFS struct {
	files     []*tarEntry
	fileIndex map[rootfs.Path]int
	tarFile   io.ReaderAt
}

var _ TarFS = (*tarFS)(nil)
var _ fs.File = (*tarFile)(nil)

func (t *tarFS) Open(name string) (fs.File, error) {
	return t.open(name)
}

func (t *tarFS) open(name string) (*tarFile, error) {
	f, _, err := t.resolveFile(name)
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
	f, _, err := t.getFile(name)
	if err != nil {
		return "", err
	}
	if f.hdr.Typeflag == tar.TypeSymlink {
		return f.hdr.Linkname, nil
	}
	return "", fs.ErrInvalid
}

func (t *tarFS) Stat(name string) (fs.FileInfo, error) {
	f, _, err := t.resolveFile(name)
	if err != nil {
		return nil, err
	}
	return f.hdr.FileInfo(), nil
}

func (t *tarFS) Lstat(name string) (fs.FileInfo, error) {
	f, _, err := t.getFile(name)
	if err != nil {
		return nil, err
	}
	return f.hdr.FileInfo(), nil
}

func (t *tarFS) ReadDir(name string) ([]fs.DirEntry, error) {
	dir, idx, err := t.resolveFile(name)
	if err != nil {
		return nil, err
	}
	if dir.hdr.Typeflag != tar.TypeDir {
		return nil, fs.ErrInvalid
	}
	dirEntries := []fs.DirEntry{}
	dirName := path.Base(name)
	idx += 1
	for idx < len(t.files) {
		file := t.files[idx]
		if path.Dir(file.hdr.Name) != dirName {
			break
		}
		dirEntries = append(dirEntries, file)
		idx += 1
	}
	return dirEntries, nil
}

func (t *tarFS) getFile(name string) (*tarEntry, int, error) {
	p, err := rootfs.NewPath(name)
	if err != nil {
		return nil, -1, err
	}
	if idx, ok := t.fileIndex[p]; ok {
		return t.files[idx], idx, nil
	}
	return nil, -1, fs.ErrNotExist
}

func (t *tarFS) resolveFile(name string) (*tarEntry, int, error) {
	path, err := rootfs.NewPath(name)
	if err != nil {
		return nil, -1, err
	}
	realpath, err := path.RealPath(t)
	if err != nil {
		return nil, -1, err
	}
	return t.getFile(realpath.TarName())
}

func New(f *os.File) (fs.FS, error) {
	tr := tar.NewReader(f)
	var files = []*tarEntry{}
	var hasRoot bool
	var result = &tarFS{tarFile: f}
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if hdr.Name == "." {
			hasRoot = true
		}
		offset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		files = append(files, &tarEntry{offset: offset, hdr: hdr})
	}
	if !hasRoot {
		hdr := &tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0755}
		files = append(files, &tarEntry{offset: 0, hdr: hdr})
	}
	// fix file order by name (directory first)
	slices.SortFunc(files, func(a, b *tarEntry) int {
		if a.hdr.Name < b.hdr.Name {
			return -1
		}
		if a.hdr.Name > b.hdr.Name {
			return 1
		}
		return 0
	})
	var fileIndex = make(map[rootfs.Path]int)
	for i, f := range files {
		p, err := rootfs.NewPath(f.hdr.Name)
		if err != nil {
			return nil, err
		}
		fileIndex[p] = i
	}
	result.files = files
	result.fileIndex = fileIndex
	return result, nil
}
