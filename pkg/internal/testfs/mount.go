// Package testfs provides filesystem fixtures for tests.
package testfs

import (
	"io/fs"
	"strings"
)

type mountFS struct {
	base    fs.FS
	prefix  string
	mounted fs.FS
}

// Mount overlays mounted at prefix within base.
func Mount(base fs.FS, prefix string, mounted fs.FS) fs.FS {
	return mountFS{base: base, prefix: prefix, mounted: mounted}
}

func (m mountFS) source(name string) (fs.FS, string) {
	if name == m.prefix {
		return m.mounted, "."
	}
	if tail, ok := strings.CutPrefix(name, m.prefix+"/"); ok {
		return m.mounted, tail
	}
	return m.base, name
}

func (m mountFS) Open(name string) (fs.File, error) {
	fss, path := m.source(name)
	return fss.Open(path)
}

func (m mountFS) Lstat(name string) (fs.FileInfo, error) {
	fss, path := m.source(name)
	return fs.Lstat(fss, path)
}

func (m mountFS) ReadLink(name string) (string, error) {
	fss, path := m.source(name)
	return fs.ReadLink(fss, path)
}
