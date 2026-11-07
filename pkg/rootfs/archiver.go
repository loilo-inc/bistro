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
	// Add adds a file with the given content and mode to the root filesystem.
	Add(path Path, content []byte, mode fs.FileMode) error
	// Close finalizes the root filesystem, writing any remaining data.
	Close() error
}

type archiver struct {
	symlink  *symlinkResolver
	copied   util.Map[Path, bool]
	excluder *excluder
	tw       *tar.Writer
	fs       fs.FS
}

type resolvingState struct {
	path      Path
	remaining string
}

var _ Archiver = &archiver{}

type ArchiverOptions struct {
	Excludes []Path
}

func NewArchiver(fs fs.FS, out io.Writer, opts ArchiverOptions) Archiver {
	r := &archiver{
		tw:       tar.NewWriter(out),
		fs:       fs,
		symlink:  newSymlinkResolver(fs),
		excludes: opts.Excludes,
	}
	return r
}

func (r *archiver) Copy(path Path) error {
	return r.symlink.Visit(path, r.shouldCopyFile, r.copyOne)
}

func (r *archiver) Add(path Path, content []byte, mode fs.FileMode) error {
	if path.IsExcluded(r.excludes) {
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

func (r *archiver) shouldCopyFile(path Path) bool {
	return !path.IsExcluded(r.excludes)
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
	if !lstat.Mode().IsRegular() {
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
