package bistro

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type RootFS interface {
	// Copy copies the file or directory at the given absolute path into the root filesystem.
	Copy(path string) error
	// Add adds a file with the given content and mode to the root filesystem.
	Add(path string, content []byte, mode os.FileMode) error
	// Close finalizes the root filesystem, writing any remaining data.
	Close() error
}

type rootFS struct {
	copied    map[string]struct{}
	resolving map[string]struct{}
	tw        *tar.Writer
}

var _ RootFS = &rootFS{}

func NewRootFS(out io.Writer) RootFS {
	return &rootFS{
		tw:        tar.NewWriter(out),
		copied:    make(map[string]struct{}),
		resolving: make(map[string]struct{}),
	}
}

func (r *rootFS) Copy(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	var paths []string
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		paths = append(paths, p)
		if p == string(filepath.Separator) {
			break
		}
	}
	for _, subdir := range slices.Backward(paths) {
		if terminate, err := r.handleSubdir(subdir, path); err != nil {
			return err
		} else if terminate {
			break
		}
	}
	return nil
}

func (r *rootFS) Add(path string, content []byte, mode os.FileMode) error {
	header := &tar.Header{
		Name:    strings.TrimPrefix(filepath.ToSlash(path), "/"),
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

func (r *rootFS) Close() error {
	return r.tw.Close()
}

func (r *rootFS) handleSubdir(path string, origPath string) (terminate bool, err error) {
	lstat, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	isSymlink := lstat.Mode()&os.ModeSymlink != 0
	if isSymlink {
		link, err := os.Readlink(path)
		if err != nil {
			return false, err
		}
		if err := r.copyOne(path, link, lstat); err != nil {
			return false, err
		}
		remaining, _ := filepath.Rel(path, origPath)
		if err := r.copyLink(path, link, remaining); err != nil {
			return false, err
		}
		// terminate the loop after handling a symlink
		return true, nil
	}
	return false, r.copyOne(path, "", lstat)
}

func (r *rootFS) copyLink(path string, link string, remaining string) error {
	if _, ok := r.resolving[path]; ok {
		return errors.New("circular symlink detected")
	}
	r.resolving[path] = struct{}{}
	defer delete(r.resolving, path)

	target := link
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	target = filepath.Clean(target)

	return r.Copy(filepath.Join(target, remaining))
}

func (r *rootFS) copyOne(path string, link string, lstat os.FileInfo) (err error) {
	defer func() {
		if err == nil {
			r.copied[path] = struct{}{}
		}
	}()
	if _, ok := r.copied[path]; ok {
		return nil
	}

	header, err := tar.FileInfoHeader(lstat, link)
	if err != nil {
		return err
	}
	header.Name = strings.TrimPrefix(filepath.ToSlash(path), "/")
	if header.Name == "" {
		header.Name = "."
	}
	if err := r.tw.WriteHeader(header); err != nil {
		return err
	}
	if !lstat.Mode().IsRegular() {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(r.tw, file); err != nil {
		return err
	}
	return nil
}
