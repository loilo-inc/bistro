package rootfs

import (
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/util"
)

type Traverser interface {
	// Walk starts walking the given path, resolving symlinks along the way.
	Walk(path Path) error
	// Traverse starts traversing the given path recursively, resolving symlinks along the way.
	Traverse(path Path) error
}

type traverser struct {
	fs      fs.FS
	walker  *symlinkWalker
	visited util.Map[Path, bool]
}

var _ Traverser = &traverser{}

func NewTraverser(fs fs.FS,
	onStart func(file Path) (bool, error),
	onVisit func(file Path, link string, stat fs.FileInfo) error,
) *traverser {
	return &traverser{fs: fs, walker: newSymlinkVisitor(fs, onStart, onVisit)}
}

func (t *traverser) Walk(path Path) error {
	_, err := t.walker.Resolve(path)
	return err
}

func (t *traverser) Traverse(path Path) error {
	if t.visited.Has(path) {
		return nil
	}
	// walk the symlink and find the destination of the path
	dest, err := t.walker.Resolve(path)
	if err != nil {
		return err
	}
	// walking was canceled or the path should be skipped
	if dest == nil {
		return nil
	}
	if t.visited.Has(*dest) {
		return nil
	}
	t.visited.Set(*dest, true)
	// check if the path is a directory. Expect dest is not symlink.
	lstat, err := fs.Lstat(t.fs, dest.Path())
	if err != nil {
		return err
	}
	// if it is a directory, handle it recursively.
	if lstat.IsDir() {
		entries, err := fs.ReadDir(t.fs, dest.Path())
		if err != nil {
			return err
		}
		for _, entry := range entries {
			entryPath := dest.Join(entry.Name())
			if err := t.Traverse(entryPath); err != nil {
				return err
			}
		}
	}
	return nil
}
