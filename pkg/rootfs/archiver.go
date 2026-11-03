package rootfs

import (
	"archive/tar"
	"errors"
	"io"
	"io/fs"
	"path"
	"regexp"
	"time"

	"github.com/loilo-inc/bistro/pkg/util"
)

type Archiver interface {
	// Copy copies the file or directory at the given absolute path into the root filesystem.
	Copy(path string) error
	// Add adds a file with the given content and mode to the root filesystem.
	Add(path string, content []byte, mode fs.FileMode) error
	// Close finalizes the root filesystem, writing any remaining data.
	Close() error
}

type archiver struct {
	linkResolver SymLinkResolver
	spFilePat    *regexp.Regexp
	copied       util.Map[Path, bool]
	tw           *tar.Writer
	fs           fs.FS
}

var _ Archiver = &archiver{}

func NewArchiver(fs fs.FS, out io.Writer) Archiver {
	return &archiver{
		fs:           fs,
		linkResolver: NewSymLinkResolver(fs),
		tw:           tar.NewWriter(out),
		spFilePat:    regexp.MustCompile(`^/(proc|sys)(/|$)`),
	}
}

func (r *archiver) Copy(file string) error {
	if !path.IsAbs(file) {
		return errors.New("path must be absolute")
	}
	return r.linkResolver.Visit(NewPath(file), r.shouldEvalSymLink, r.copyOne)
}

func (r *archiver) Add(path string, content []byte, mode fs.FileMode) error {
	header := &tar.Header{
		Name:    NewPath(path).TarName(),
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

func (r *archiver) shouldEvalSymLink(p Path) bool {
	return !r.spFilePat.MatchString(p.Abs())
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
