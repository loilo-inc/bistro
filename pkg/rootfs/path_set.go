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
	resolvedIdx   int
}

func pathMatch(p Path, list []Path) bool {
	for _, prefix := range list {
		excluded := prefix.Abs()
		if p.Abs() == excluded ||
			strings.HasPrefix(p.Abs(), strings.TrimSuffix(excluded, "/")+"/") {
			return true
		}
	}
	return false
}

// Resolve resolves the original paths to their real paths using the provided filesystem.
func (ps *PathSet) Resolve(fss fs.FS) error {
	for i := ps.resolvedIdx; i < len(ps.origPaths); i++ {
		p := ps.origPaths[i]
		dir := p.Dir()
		realDir, err := dir.RealPath(fss)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			goto next
		case err != nil:
			return err
		case realDir.Abs() != dir.Abs():
			ps.resolvedPaths = append(ps.resolvedPaths, realDir.Join(p.Base()))
		}
	next:
		ps.resolvedIdx += 1
	}
	return nil
}

// Match checks if the given path matches any of the original or resolved paths in the set.
func (ps *PathSet) Match(fss fs.FS, p Path) (bool, error) {
	if pathMatch(p, ps.origPaths) {
		return true, nil
	}
	if !ps.isResolved() {
		if err := ps.Resolve(fss); err != nil {
			return false, err
		}
	}
	if pathMatch(p, ps.resolvedPaths) {
		return true, nil
	}
	onStart := func(p Path) (bool, error) {
		if pathMatch(p, ps.origPaths) || pathMatch(p, ps.resolvedPaths) {
			// stop walk if transitive path matches the list
			return false, nil
		}
		return true, nil
	}
	dest, err := newSymlinkVisitor(fss, onStart, nil).Resolve(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case dest == nil:
		return true, nil
	default:
		return pathMatch(*dest, ps.origPaths) ||
			pathMatch(*dest, ps.resolvedPaths), nil
	}
}

func (ps *PathSet) isResolved() bool {
	return ps.resolvedIdx >= len(ps.origPaths)
}

func (ps *PathSet) Clone() PathSet {
	return PathSet{
		origPaths:     append([]Path(nil), ps.origPaths...),
		resolvedPaths: append([]Path(nil), ps.resolvedPaths...),
		resolvedIdx:   ps.resolvedIdx,
	}
}

func (ps *PathSet) Add(p ...Path) {
	ps.origPaths = append(ps.origPaths, p...)
}
