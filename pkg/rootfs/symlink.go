package rootfs

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/loilo-inc/bistro/pkg/util"
)

type symlinkResolver struct {
	resolving util.Map[resolvingState, bool]
	visited   util.Map[Path, bool]
	fs        fs.FS
	onVisit   func(file Path, link string, stat fs.FileInfo) error
	onStart   func(file Path) bool
}

func newSymlinkResolver(fs fs.FS) *symlinkResolver {
	return &symlinkResolver{fs: fs}
}

var errVisitCanceld = errors.New("visit cancelled")

func (r *symlinkResolver) Resolve(path Path) (*Path, error) {
	if r.onStart != nil && !r.onStart(path) {
		return nil, errVisitCanceld
	}
	var paths []Path
	var p Path = path
	for ; ; p = p.Dir() {
		paths = append(paths, p)
		if p.Abs() == "/" {
			break
		}
	}
	for _, subdir := range slices.Backward(paths) {
		if terminate, resolved, err := r.handleSubdir(subdir, path); err != nil {
			return nil, err
		} else if terminate {
			return resolved, nil
		}
	}
	return &path, nil
}

func (r *symlinkResolver) Visit(
	path Path,
	onStart func(file Path) bool,
	onVisit func(file Path, link string, stat fs.FileInfo) error,
) error {
	r.onStart = onStart
	r.onVisit = onVisit
	defer func() {
		r.onStart = nil
		r.onVisit = nil
	}()
	_, err := r.Resolve(path)
	if errors.Is(err, errVisitCanceld) {
		return nil
	}
	return err
}

func (r *symlinkResolver) handleSubdir(file Path, origPath Path) (bool, *Path, error) {
	lstat, err := fs.Lstat(r.fs, file.Path())
	if err != nil {
		return false, nil, err
	}
	isSymlink := lstat.Mode()&fs.ModeSymlink != 0
	if isSymlink {
		link, err := fs.ReadLink(r.fs, file.Path())
		if err != nil {
			return false, nil, err
		}
		if err := r.callOnVisit(file, link, lstat); err != nil {
			return false, nil, err
		}
		remaining := strings.TrimPrefix(origPath.Abs(), file.Abs())
		resolved, err := r.handleLink(file, link, remaining)
		if err != nil {
			return false, nil, err
		}
		// terminate the loop after handling a symlink
		return true, resolved, nil
	}
	if err := r.callOnVisit(file, "", lstat); err != nil {
		return false, nil, err
	}
	return false, &file, nil
}

func (r *symlinkResolver) handleLink(file Path, link string, remaining string) (*Path, error) {
	state := resolvingState{path: file, remaining: remaining}
	if r.resolving.Has(state) {
		return nil, fmt.Errorf("circular symlink detected: %s -> %s", file.Abs(), link)
	}
	if r.resolving.Size() >= 256 {
		return nil, fmt.Errorf("too many symlinks being resolved: %s", file.Abs())
	}
	r.resolving.Set(state, true)
	defer r.resolving.Delete(state)

	target, err := r.walk(file.Dir(), link)
	if err != nil {
		return nil, err
	}
	return r.Resolve(target.Join(remaining))
}

func (r *symlinkResolver) walk(dir Path, link string) (*Path, error) {
	var curr Path
	if path.IsAbs(link) {
		curr = MustPath("/")
	} else {
		curr = dir
	}
	for comp := range strings.SplitSeq(link, "/") {
		switch comp {
		case "", ".":
			// skip
		case "..":
			if curr.Abs() == "/" {
				return nil, fmt.Errorf("[rootfs/symlink] upper root symlink detected: %s", curr.Abs())
			}
			if v, err := r.Resolve(curr.Dir()); err != nil {
				return nil, err
			} else {
				curr = *v
			}
		default:
			if v, err := r.Resolve(curr.Join(comp)); err != nil {
				return nil, err
			} else {
				curr = *v
			}
		}
	}
	return &curr, nil
}

func (r *symlinkResolver) callOnVisit(file Path, link string, stat fs.FileInfo) error {
	if r.onVisit == nil {
		return nil
	}
	if r.visited.Has(file) {
		return nil
	}
	err := r.onVisit(file, link, stat)
	if err != nil {
		return err
	}
	r.visited.Set(file, true)
	return nil
}
