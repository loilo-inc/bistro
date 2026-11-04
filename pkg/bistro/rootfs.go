package bistro

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type RootFS interface {
	// Copy copies the file or directory at the given absolute path into the root filesystem.
	Copy(path rootfs.Path) error
	// Add adds a file with the given content and mode to the root filesystem.
	Add(path rootfs.Path, content []byte, mode os.FileMode) error
	// Close finalizes the root filesystem, writing any remaining data.
	Close() error
}

type rootFS struct {
	copied    map[rootfs.Path]struct{}
	resolving map[resolvingState]struct{}
	spFilePat *regexp.Regexp
	tw        *tar.Writer
}

type resolvingState struct {
	path      rootfs.Path
	remaining string
}

var _ RootFS = &rootFS{}

func NewRootFS(out io.Writer) RootFS {
	return &rootFS{
		tw:        tar.NewWriter(out),
		copied:    make(map[rootfs.Path]struct{}),
		resolving: make(map[resolvingState]struct{}),
		spFilePat: regexp.MustCompile(`^/(proc|sys)(/|$)`),
	}
}

func (r *rootFS) Copy(path rootfs.Path) error {
	var paths []rootfs.Path
	var p rootfs.Path = path
	for ; ; p = p.Dir() {
		paths = append(paths, p)
		if p.Abs() == "/" {
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

func (r *rootFS) Add(path rootfs.Path, content []byte, mode os.FileMode) error {
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

func (r *rootFS) Close() error {
	return r.tw.Close()
}

func (r *rootFS) handleSubdir(file rootfs.Path, origPath rootfs.Path) (terminate bool, err error) {
	lstat, err := os.Lstat(file.Abs())
	if err != nil {
		return false, err
	}
	isSymlink := lstat.Mode()&os.ModeSymlink != 0
	if isSymlink {
		link, err := os.Readlink(file.Abs())
		if err != nil {
			return false, err
		}
		if err := r.copyOne(file, link, lstat); err != nil {
			return false, err
		}
		remaining := strings.TrimPrefix(origPath.Abs(), file.Abs())
		if err := r.copyLink(file, link, remaining); err != nil {
			return false, err
		}
		// terminate the loop after handling a symlink
		return true, nil
	}
	return false, r.copyOne(file, "", lstat)
}

func (r *rootFS) copyLink(file rootfs.Path, link string, remaining string) error {
	state := resolvingState{path: file, remaining: remaining}
	if _, ok := r.resolving[state]; ok {
		return fmt.Errorf("circular symlink detected: %s -> %s", file.Abs(), link)
	}
	if len(r.resolving) >= 256 {
		return fmt.Errorf("too many symlinks being resolved")
	}
	r.resolving[state] = struct{}{}
	defer delete(r.resolving, state)

	var target rootfs.Path
	if path.IsAbs(link) {
		target = rootfs.MustPath(link)
	} else {
		// prohibit symlink to outside of the root filesystem
		untrusted := path.Join(file.Dir().Path(), link)
		v, err := rootfs.NewPath(untrusted)
		if err != nil {
			return err
		}
		target = v
	}
	// terminate early for special file prefixes like /proc and /sys
	if r.spFilePat.MatchString(target.Abs()) {
		return nil
	}
	return r.Copy(target.Join(remaining))
}

func (r *rootFS) copyOne(path rootfs.Path, link string, lstat os.FileInfo) (err error) {
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
	file, err := os.Open(path.Abs())
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(r.tw, file); err != nil {
		return err
	}
	return nil
}
