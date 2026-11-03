package rootfs

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/loilo-inc/bistro/pkg/util"
)

type SymLinkResolver interface {
	Resolve(p Path) (*Path, error)
	Visit(p Path, onStart func(Path) bool, onVisit func(Path, string, fs.FileInfo) error) error
}

type symLinkResolver struct {
	resolving util.Map[resolvingState, bool]
	fs        fs.FS
}

type resolvingState struct {
	path      Path
	remaining string
}

type symLinkVisitor struct {
	*symLinkResolver
	onStart func(Path) bool
	onVisit func(Path, string, fs.FileInfo) error
}

func NewSymLinkResolver(fs fs.FS) SymLinkResolver {
	return &symLinkResolver{fs: fs}
}

func (r *symLinkResolver) Visit(p Path,
	onStart func(Path) bool,
	onVisit func(Path, string, fs.FileInfo) error) error {
	rv := &symLinkVisitor{
		symLinkResolver: r,
		onStart:         onStart,
		onVisit:         onVisit,
	}
	_, err := rv.start(p)
	return err
}

func (r *symLinkResolver) Resolve(p Path) (*Path, error) {
	rv := &symLinkVisitor{symLinkResolver: r}
	return rv.start(p)
}

func (r *symLinkVisitor) start(origPath Path) (*Path, error) {
	if r.onStart != nil && !r.onStart(origPath) {
		return nil, nil
	}
	var dirs []Path
	curr := origPath
	for ; ; curr = curr.Dir() {
		dirs = append(dirs, curr)
		if curr.Abs() == "/" {
			break
		}
	}
	for _, dir := range slices.Backward(dirs) {
		continu, resolved, err := r.handle(dir, origPath)
		if err != nil {
			return nil, err
		}
		// if a symlink was resolved, return it
		if !continu {
			return resolved, nil
		}
	}
	// no symlink encountered, return the original path
	return &origPath, nil
}

func (r *symLinkVisitor) handle(path Path, origPath Path) (bool, *Path, error) {
	lstat, err := fs.Lstat(r.fs, path.Path())
	if err != nil {
		return false, nil, err
	}
	isSymlink := lstat.Mode()&fs.ModeSymlink != 0
	if isSymlink {
		link, err := fs.ReadLink(r.fs, path.Path())
		if err != nil {
			return false, nil, err
		}
		if err := r.callOnVisit(path, link, lstat); err != nil {
			return false, nil, err
		}
		remaining := strings.TrimPrefix(origPath.Abs(), path.Abs())
		if resolved, err := r.handleLink(path, link, remaining); err != nil {
			return false, nil, err
		} else {
			return false, resolved, nil
		}
	}
	return true, &path, r.callOnVisit(path, "", lstat)
}

func (r *symLinkVisitor) handleLink(p Path, link string, remaining string) (*Path, error) {
	state := resolvingState{path: p, remaining: remaining}
	if r.resolving.Has(state) {
		return nil, fmt.Errorf("[rootfs/path] circular symlink detected: %s", p.Path())
	}
	if r.resolving.Size() >= 256 {
		return nil, fmt.Errorf("[rootfs/path] too many symlinks being resolved: %s", p.Path())
	}
	r.resolving.Set(state, true)
	defer r.resolving.Delete(state)
	var target Path
	if path.IsAbs(link) {
		target = NewPath(link)
	} else {
		target = p.Dir().Join(link)
	}
	return r.start(target.Join(remaining))
}

func (r *symLinkVisitor) callOnVisit(p Path, link string, lstat fs.FileInfo) error {
	if r.onVisit != nil {
		return r.onVisit(p, link, lstat)
	}
	return nil
}
