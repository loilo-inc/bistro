package rootfs

import (
	"io"
	"io/fs"
)

type FsReaderAt struct {
	File fs.File
}

func (r *FsReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if ra, ok := r.File.(io.ReaderAt); ok {
		return ra.ReadAt(p, off)
	}
	return 0, fs.ErrInvalid
}
