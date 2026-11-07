package rootfs

import (
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/util"
)

type fileTraverser struct {
	fs      fs.FS
	visited util.Map[Path, bool]
}

func newFileTraverser(fs fs.FS) *fileTraverser {
	return &fileTraverser{fs: fs}
}

func (t *fileTraverser) Walk(
	path Path,
	onStart func(path Path) (bool, error),
	onVisit func(path Path, link string, info fs.FileInfo) error,
) error {
	_, err := newSymlinkVisitor(t.fs, onStart, onVisit).Resolve(path)
	return err
}

func (t *fileTraverser) Traverse(
	path Path,
	onStart func(path Path) (bool, error),
	onVisit func(path Path, link string, info fs.FileInfo) error,
) error {
	if t.visited.Has(path) {
		return nil
	}
	sym := newSymlinkVisitor(t.fs, onStart, onVisit)
	// walk the symlink and find the destination of the path
	dest, err := sym.Resolve(path)
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
			if err := t.Traverse(entryPath, onStart, onVisit); err != nil {
				return err
			}
		}
	}
	return nil
}
