package rootfs

import (
	"errors"
	"io/fs"
	"strings"
)

func NewPathSet(list []Path) PathSet {
	result := PathSet{origPaths: list}
	return result
}

type PathSet struct {
	origPaths     []Path
	resolvedPaths []Path
	resolved      bool
}

func pathMatches(p Path, list []Path) bool {
	for _, prefix := range list {
		excluded := prefix.Abs()
		if p.Abs() == excluded ||
			strings.HasPrefix(p.Abs(), strings.TrimSuffix(excluded, "/")+"/") {
			return true
		}
	}
	return false
}

func (ps *PathSet) resolveLinks(fss fs.FS) error {
	var result []Path
	for _, p := range ps.origPaths {
		dir := p.Dir()
		realDir, err := dir.RealPath(fss)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			break
		case err != nil:
			return err
		case realDir.Abs() != dir.Abs():
			result = append(result, realDir.Join(p.Base()))
		}
		result = append(result, p)
	}
	ps.resolvedPaths = result
	ps.resolved = true
	return nil
}

func (ps *PathSet) LMatches(fss fs.FS, p Path) (bool, error) {
	if pathMatches(p, ps.origPaths) {
		return true, nil
	}
	if !ps.resolved {
		if err := ps.resolveLinks(fss); err != nil {
			return false, err
		}
	}
	real, err := p.RealPath(fss)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		break
	case err != nil:
		return false, err
	default:
		return pathMatches(*real, ps.resolvedPaths), nil
	}
	return false, nil
}

func (ps *PathSet) Clone() PathSet {
	return PathSet{
		origPaths:     append([]Path(nil), ps.origPaths...),
		resolvedPaths: append([]Path(nil), ps.resolvedPaths...),
		resolved:      ps.resolved,
	}
}

func (ps *PathSet) Add(p ...Path) {
	ps.origPaths = append(ps.origPaths, p...)
	ps.resolved = false
}
