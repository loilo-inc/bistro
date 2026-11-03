package bistro

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type RootFS interface {
	// Copy copies the file or directory at the given absolute path into the root filesystem.
	Copy(path string) error
	// Add adds a file with the given content and mode to the root filesystem.
	Add(path string, content []byte, mode fs.FileMode) error
	// Close finalizes the root filesystem, writing any remaining data.
	Close() error
}

type rootFS struct {
	copied    util.Map[rootfs.Path, bool]
	resolving util.Map[resolvingState, bool]
	spFilePat *regexp.Regexp
	tw        *tar.Writer
	fs        fs.FS
}

type resolvingState struct {
	path      rootfs.Path
	remaining string
}

var _ RootFS = &rootFS{}

func NewRootFS(fs fs.FS, out io.Writer) RootFS {
	return &rootFS{
		fs:        fs,
		tw:        tar.NewWriter(out),
		spFilePat: regexp.MustCompile(`^/(proc|sys)(/|$)`),
	}
}

func (r *rootFS) Copy(file string) error {
	if !path.IsAbs(file) {
		return errors.New("path must be absolute")
	}
	origPath := rootfs.NewPath(file)
	var paths []rootfs.Path
	for p := path.Clean(file); ; p = path.Dir(p) {
		paths = append(paths, rootfs.NewPath(p))
		if p == "/" {
			break
		}
	}
	for _, subdir := range slices.Backward(paths) {
		if terminate, err := r.handleSubdir(subdir, origPath); err != nil {
			return err
		} else if terminate {
			break
		}
	}
	return nil
}

func (r *rootFS) Add(path string, content []byte, mode fs.FileMode) error {
	header := &tar.Header{
		Name:    rootfs.NewPath(path).TarName(),
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

func (r *rootFS) handleSubdir(path rootfs.Path, origPath rootfs.Path) (terminate bool, err error) {
	lstat, err := fs.Lstat(r.fs, path.Path())
	if err != nil {
		return false, err
	}
	isSymlink := lstat.Mode()&fs.ModeSymlink != 0
	if isSymlink {
		link, err := fs.ReadLink(r.fs, path.Path())
		if err != nil {
			return false, err
		}
		if err := r.copyOne(path, link, lstat); err != nil {
			return false, err
		}
		remaining := strings.TrimPrefix(origPath.Abs(), path.Abs())
		if err := r.copyLink(path, link, remaining); err != nil {
			return false, err
		}
		// terminate the loop after handling a symlink
		return true, nil
	}
	return false, r.copyOne(path, "", lstat)
}

func (r *rootFS) copyLink(vPath rootfs.Path, link string, remaining string) error {
	state := resolvingState{path: vPath, remaining: remaining}
	if r.resolving.Has(state) {
		return fmt.Errorf("circular symlink detected: %s -> %s", vPath, link)
	}
	if r.resolving.Size() >= 256 {
		return fmt.Errorf("too many symlinks being resolved")
	}
	r.resolving.Set(state, true)
	defer r.resolving.Delete(state)

	var target rootfs.Path
	if path.IsAbs(link) {
		target = rootfs.NewPath(link)
	} else {
		target = vPath.Dir().Join(target.Path())
	}
	// terminate early for special file prefixes like /proc and /sys
	if r.spFilePat.MatchString(target.Abs()) {
		return nil
	}
	return r.Copy(target.Join(remaining).Abs())
}

func (r *rootFS) copyOne(path rootfs.Path, link string, lstat fs.FileInfo) (err error) {
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
