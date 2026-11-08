package rootfs

import (
	"archive/tar"
	"io"
	"io/fs"
	"time"

	"github.com/loilo-inc/bistro/pkg/util"
)

type Archiver interface {
	// Copy copies the file or directory at the given absolute path into the root filesystem.
	Copy(path Path) error
	// CopyRecursive copies the file or directory at the given absolute path and all its contents into the root filesystem.
	CopyRecursive(path Path) error
	// Add adds a file with the given content and mode to the root filesystem.
	Add(path Path, content []byte, mode fs.FileMode) error
	// Close finalizes the root filesystem, writing any remaining data.
	Close() error
	// SetExcludes sets the paths to be excluded from the root filesystem.
	SetExcludes(excludes PathSet)
}

type archiver struct {
	traverser *traverser
	copied    util.Map[Path, bool]
	excludes  PathSet
	tw        *tar.Writer
	fs        fs.FS
}

type resolvingState struct {
	path      Path
	remaining string
}

var _ Archiver = &archiver{}

func NewArchiver(fs fs.FS, out io.Writer) Archiver {
	r := &archiver{
		tw: tar.NewWriter(out),
		fs: fs,
	}
	r.traverser = NewTraverser(fs, r.shouldCopyFile, r.copyOne)
	return r
}

func (r *archiver) SetExcludes(excludes PathSet) {
	r.excludes = excludes
}

func (r *archiver) Copy(path Path) error {
	return r.traverser.Walk(path)
}

func (r *archiver) CopyRecursive(path Path) error {
	return r.traverser.Traverse(path)
}

func (r *archiver) Add(path Path, content []byte, mode fs.FileMode) error {
	if skip, err := r.excludes.Match(r.fs, path); err != nil {
		return err
	} else if skip {
		return nil
	}
	header := &tar.Header{
		Name:    path.TarName(),
		Mode:    int64(mode),
		Size:    int64(len(content)),
		ModTime: time.Now(),
	}
	if err := r.tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := r.tw.Write(content)
	return err
}

func (r *archiver) Close() error {
	return r.tw.Close()
}

func (r *archiver) shouldCopyFile(path Path) (bool, error) {
	skip, err := r.excludes.Match(r.fs, path)
	if err != nil {
		return false, err
	}
	return !skip, nil
}

func (r *archiver) copyOne(path Path, link string, lstat fs.FileInfo) (err error) {
	defer func() {
		if err == nil {
			r.copied.Set(path, true)
		}
	}()
	if r.copied.Has(path) {
		return nil
	}
	// only archive regular files, directories, and symlinks
	mode := lstat.Mode()
	if !mode.IsDir() && !mode.IsRegular() && mode&fs.ModeSymlink == 0 {
		return nil
	}
	header, err := tar.FileInfoHeader(lstat, link)
	if err != nil {
		return err
	}
	header.Name = path.TarName()
	if header.Name == "" {
		header.Name = "."
	}
	if err := r.tw.WriteHeader(header); err != nil {
		return err
	}
	if !mode.IsRegular() {
		return nil
	}
	file, err := r.fs.Open(path.Path())
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(r.tw, file); err != nil {
		return err
	}
	return nil
}
