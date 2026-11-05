package bistro

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
)

type Unpacker interface {
	Unpack(tarFile string) error
}

type unpacker struct {
	dest string
}

func NewUnpacker(dest string) Unpacker {
	return &unpacker{dest: dest}
}

func (u *unpacker) Unpack(src string) error {
	tarFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer tarFile.Close()

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

	if err := u.extract(root, tarFile); err != nil {
		return err
	}
	if err := root.Close(); err != nil {
		return err
	}
	return tarFile.Close()
}

func (u *unpacker) extract(root *os.Root, tarFile *os.File) error {
	if _, err := tarFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	tr := tar.NewReader(tarFile)

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
		}

		if !dirToDir {
			if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
				return err
			}
		}

		mode := hdr.FileInfo().Mode()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if !dirToDir {
				// NOTE: dir entries must have the writable permission for later entries to be created inside them
				if err := root.Mkdir(name, mode.Perm()); err != nil {
					return err
				}
				if err := root.Chmod(name, mode); err != nil {
					return err
				}
			}
		case tar.TypeSymlink:
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := u.handleFile(root, name, mode, tr); err != nil {
				return err
			}
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
	// create the file with initial permissions 0600 (read/write for owner only)
	outFile, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
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
