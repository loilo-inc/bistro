package rootfs

import (
	"errors"
	"io/fs"
	"strings"
)

type excluder struct {
	excludes *pathSet
	resolved bool
	symlink  *symlinkResolver
}

func NewExcluder(fsys fs.FS, excludes []Path) (*excluder, error) {
	symlink := newSymlinkResolver(fsys)
	ps, err := newPathSet(excludes, symlink)
	if err != nil {
		return nil, err
	}
	return &excluder{
		excludes: ps,
		resolved: false,
		symlink:  symlink,
	}, nil
}

func (e *excluder) IsExcluded(target Path) bool {
	ps, err := newPathSet([]Path{target}, e.symlink)
	if err != nil {
		return false
	}
	return ps.isExcluded(e.excludes)
}

func newPathSet(list []Path, sym *symlinkResolver) (*pathSet, error) {
	var result pathSet
	for _, p := range list {
		dir := p.Dir()
		realDir, err := sym.Resolve(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			break
		case err != nil:
			return nil, err
		case realDir.Abs() != dir.Abs():
			result.paths = append(result.paths, realDir.Join(p.Base()))
		}
		result.paths = append(result.paths, p)
	}
	return &result, nil
}

type pathSet struct {
	paths []Path
}

func (ps *pathSet) isExcluded(targets *pathSet) bool {
	for _, p := range ps.paths {
		target := p.Abs()
		for _, prefix := range targets.paths {
			excluded := prefix.Abs()
			if target == excluded ||
				strings.HasPrefix(target, strings.TrimSuffix(excluded, "/")+"/") {
				return true
			}
		}
	}
	return false
}
