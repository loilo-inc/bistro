package tarfs

import (
	"archive/tar"
	"io"
	"io/fs"
	"maps"
	"slices"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
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
	return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrInvalid}
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
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
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
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
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
	var result = &tarFS{tarFile: f}
	var missingDirs = util.Map[rootfs.Path, *tarEntry]{}
	var hardLinks []*tarEntry
	var hasRoot bool

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
		missingDirs.Delete(path)

		if hdr.Typeflag == tar.TypeLink {
			hardLinks = append(hardLinks, file)
		}

		if path.IsRoot() {
			hasRoot = true
		}

		// Ensure all ancestor directories exist in files or missingDirs.
		for dir := path.Dir(); !dir.IsRoot(); dir = dir.Dir() {
			if _, ok := files[dir]; !ok && !missingDirs.Has(dir) {
				hdr := &tar.Header{
					Name:     dir.TarName(),
					Typeflag: tar.TypeDir,
					Mode:     0755,
				}
				missingDirs.Set(dir, &tarEntry{path: dir, hdr: hdr})
			}
		}
	}

	if !hasRoot {
		path := rootfs.MustPath("/")
		missingDirs.Set(path, &tarEntry{
			path: path,
			hdr:  &tar.Header{Name: path.TarName(), Typeflag: tar.TypeDir, Mode: 0755},
		})
	}

	resolvedHardLinks := make(map[rootfs.Path]*tarEntry)
	for _, link := range hardLinks {
		// link has been replaced by another entry, skip it
		if files[link.path] != link {
			continue
		}
		var curr = link
		visited := util.Map[rootfs.Path, bool]{}
		for curr.hdr.Typeflag == tar.TypeLink {
			// too many links
			if visited.Size() > 256 {
				return nil, &fs.PathError{Op: "link", Path: link.path.Path(), Err: fs.ErrInvalid}
			}
			// cycle
			if visited.Has(curr.path) {
				return nil, &fs.PathError{Op: "link", Path: link.path.Path(), Err: fs.ErrInvalid}
			}
			visited.Set(curr.path, true)
			linkname, err := rootfs.NewPath(curr.hdr.Linkname)
			if err != nil {
				return nil, err
			}
			next, ok := files[linkname]
			if !ok {
				return nil, &fs.PathError{Op: "link", Path: link.path.Path(), Err: fs.ErrNotExist}
			}
			curr = next
		}
		newHdr := *curr.hdr
		newHdr.Name = link.hdr.Name
		newEntry := &tarEntry{
			offset: curr.offset,
			path:   link.path,
			hdr:    &newHdr,
		}
		resolvedHardLinks[link.path] = newEntry
	}
	maps.Copy(files, resolvedHardLinks)

	maps.Insert(files, missingDirs.Iter())

	var dirEntries = make(map[rootfs.Path][]fs.DirEntry)
	for path, file := range files {
		if !path.IsRoot() {
			dir := path.Dir()
			dirEntries[dir] = append(dirEntries[dir], file)
		}
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
