package bistro

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/loilo-inc/bistro/pkg/util"
)

type Unpacker interface {
	Unpack(tarFile string) error
}

type unpacker struct {
	dest      string
	chmodDirs util.Map[string, chmodEntiry]
}

type chmodEntiry struct {
	name string
	mode os.FileMode
}

func NewUnpacker(dest string) Unpacker {
	return &unpacker{dest: dest}
}

func (u *unpacker) Unpack(tarFile string) error {
	defer u.reset()

	destFile, err := os.Open(tarFile)
	if err != nil {
		return err
	}
	defer destFile.Close()

	destLstat, err := os.Lstat(u.dest)
	switch {
	case err != nil && !os.IsNotExist(err):
		return err
	case err == nil && !destLstat.IsDir():
		return fmt.Errorf("[unpacker] destination is not a directory: %s", u.dest)
	case os.IsNotExist(err):
		if err := os.MkdirAll(u.dest, 0755); err != nil {
			return err
		}
	}

	root, err := os.OpenRoot(u.dest)
	if err != nil {
		return err
	}
	defer root.Close()

	extractErr := u.extract(root, destFile)
	fixErr := u.fixDirModes(root)
	if extractErr != nil {
		return extractErr
	}
	if fixErr != nil {
		return fixErr
	}
	if err := root.Close(); err != nil {
		return err
	}
	return destFile.Close()
}

func (u *unpacker) reset() {
	u.chmodDirs.Clear()
}

func (u *unpacker) extract(root *os.Root, destFile *os.File) error {

	tr := tar.NewReader(destFile)

	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
		case tar.TypeSymlink:
		case tar.TypeReg:
		default:
			return fmt.Errorf("[unpacker] unsupported tar entry type: %c", hdr.Typeflag)
		}

		// prohibit escaping the destination directory
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("[unpacker] invalid target path: %s", hdr.Name)
		}
		name := filepath.Clean(hdr.Name)

		info, err := root.Lstat(name)
		var dirToDir bool
		switch {
		case os.IsNotExist(err):
			// Nothing to remove.
		case err != nil:
			return err
		case info.IsDir() && hdr.Typeflag == tar.TypeDir:
			// Merge directories.
			dirToDir = true
		default:
			if err := root.Remove(name); err != nil {
				return err
			}
			if info.IsDir() {
				u.chmodDirs.Delete(name)
			}
		}

		if !dirToDir {
			if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
				return err
			}
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			mode := hdr.FileInfo().Mode()
			if !dirToDir {
				// create the directory with writable permissions first for later entries
				if err := root.Mkdir(name, mode.Perm()|0700); err != nil {
					return err
				}
			} else {
				// temporarily set writable permissions for the existing directory
				if err := root.Chmod(name, mode.Perm()|0700); err != nil {
					return err
				}
			}
			u.chmodDirs.Set(name, chmodEntiry{name: name, mode: mode})
		case tar.TypeSymlink:
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := u.handleFile(root, name, hdr.FileInfo().Mode(), tr); err != nil {
				return err
			}
		}
	}
	return nil
}

func (u *unpacker) fixDirModes(root *os.Root) error {
	var chmodEntries []chmodEntiry
	for _, v := range u.chmodDirs.Iter() {
		chmodEntries = append(chmodEntries, v)
	}
	// Apply chmod for merged directories in order of depth (deepest first)
	slices.SortFunc(chmodEntries, func(a, b chmodEntiry) int {
		aDepth := strings.Count(a.name, "/")
		bDepth := strings.Count(b.name, "/")
		if a.name == "." {
			aDepth = -1
		}
		if b.name == "." {
			bDepth = -1
		}
		return bDepth - aDepth
	})
	for _, entry := range chmodEntries {
		if err := root.Chmod(entry.name, entry.mode); err != nil {
			return err
		}
	}
	return nil
}

func (u *unpacker) handleFile(
	root *os.Root,
	name string,
	mode os.FileMode,
	tr *tar.Reader,
) error {
	outFile, err := root.Create(name)
	if err != nil {
		return err
	}
	defer outFile.Close()
	if _, err = io.Copy(outFile, tr); err != nil {
		return err
	}
	if err := outFile.Chmod(mode); err != nil {
		return err
	}
	return outFile.Close()
}
