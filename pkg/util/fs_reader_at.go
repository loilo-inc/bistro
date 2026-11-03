package util

import (
	"io"
	"io/fs"
)

type fsReaderAt struct {
	r fs.File
}

func NewReaderAt(r fs.File) io.ReaderAt {
	return &fsReaderAt{r: r}
}

func (f *fsReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if ra, ok := f.r.(io.ReaderAt); ok {
		return ra.ReadAt(p, off)
	}
	return 0, fs.ErrInvalid
}
