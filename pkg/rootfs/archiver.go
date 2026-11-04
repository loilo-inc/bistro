package rootfs

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
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
	copied    map[Path]struct{}
	resolving map[resolvingState]struct{}
	spFilePat *regexp.Regexp
	tw        *tar.Writer
	fs        fs.FS
}

type resolvingState struct {
	path      Path
	remaining string
}

var _ Archiver = &archiver{}

func NewArchiver(out io.Writer) Archiver {
	return &archiver{
		tw:        tar.NewWriter(out),
		copied:    make(map[Path]struct{}),
		resolving: make(map[resolvingState]struct{}),
		spFilePat: regexp.MustCompile(`^/(proc|sys)(/|$)`),
	}
}

func (r *archiver) Copy(path Path) error {
	var paths []Path
	var p Path = path
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

func (r *archiver) Add(path Path, content []byte, mode fs.FileMode) error {
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

func (r *archiver) handleSubdir(file Path, origPath Path) (terminate bool, err error) {
	lstat, err := fs.Lstat(r.fs, file.Path())
	if err != nil {
		return false, err
	}
	isSymlink := lstat.Mode()&fs.ModeSymlink != 0
	if isSymlink {
		link, err := fs.ReadLink(r.fs, file.Path())
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

func (r *archiver) copyLink(file Path, link string, remaining string) error {
	state := resolvingState{path: file, remaining: remaining}
	if _, ok := r.resolving[state]; ok {
		return fmt.Errorf("circular symlink detected: %s -> %s", file.Abs(), link)
	}
	if len(r.resolving) >= 256 {
		return fmt.Errorf("too many symlinks being resolved")
	}
	r.resolving[state] = struct{}{}
	defer delete(r.resolving, state)

	var target Path
	if path.IsAbs(link) {
		target = MustPath(link)
	} else {
		// prohibit symlink to outside of the root filesystem
		untrusted := path.Join(file.Dir().Path(), link)
		v, err := NewPath(untrusted)
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

func (r *archiver) copyOne(path Path, link string, lstat fs.FileInfo) (err error) {
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
