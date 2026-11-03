package dpkg

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"runtime"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type Query interface {
	Get(name string) (Package, error)
	List() ([]Package, error)
	Load() error
	LookUp(file string) (Package, error)
}

type query struct {
	// must be arm64/amd64
	arch     string
	packages []*pkg
	nameMap  util.Map[string, []*pkg]
	filesMap util.Map[string, *pkg]
	fs       fs.FS
}

var _ Query = &query{}

type OwnedFile struct {
	Package *Package
	File    string
}

const DefaultBaseDir = "var/lib/dpkg"

// https://manpages.debian.org/testing/dpkg/dpkg.1.en.html#Package_states
const (
	StatusInstalled       = "installed"
	StatusNotInstalled    = "not-installed"
	StatusConfigFiles     = "config-files"
	StatusHalfInstalled   = "half-installed"
	StatusHalfConfigured  = "half-configured"
	StatusTriggersAwaited = "triggers-awaited"
	StatusTriggersPending = "triggers-pending"
	StatusUnpacked        = "unpacked"
)

const (
	FlagOK        = "ok"
	FlagReinstreq = "reinstreq"
)

const (
	SelectionInstall   = "install"
	SelectionHold      = "hold"
	SelectionPurge     = "purge"
	SelectionDeinstall = "deinstall"
	SelectionUnknown   = "unknown"
)

func NewQuery(fs fs.FS) Query {
	return &query{arch: runtime.GOARCH, fs: fs}
}

func (d *query) Get(name string) (Package, error) {
	if d.packages == nil {
		return nil, errors.New("packages not loaded")
	}
	list := d.nameMap.Get(name)
	if len(list) == 0 {
		return nil, errors.New("package not found: " + name)
	}
	if len(list) == 1 {
		return list[0], nil
	}
	for _, pkg := range list {
		if pkg.Architecture() == d.arch {
			return pkg, nil
		}
	}
	return list[0], nil
}

func (d *query) List() ([]Package, error) {
	packages, err := d.list()
	if err != nil {
		return nil, err
	}
	var list []Package
	for _, pkg := range packages {
		list = append(list, pkg)
	}
	return list, nil
}

func (d *query) list() ([]*pkg, error) {
	if d.packages == nil {
		return nil, errors.New("packages not loaded")
	}
	return d.packages, nil
}

func (q *query) LookUp(searchFile string) (Package, error) {
	if !path.IsAbs(searchFile) {
		return nil, fmt.Errorf("pattern must be an absolute path: %s", searchFile)
	}
	if cache, ok := q.filesMap.LookUp(searchFile); ok {
		return cache, nil
	}
	packages, err := q.list()
	if err != nil {
		return nil, err
	}
	for _, pkg := range packages {
		files, err := pkg.ListFiles()
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			q.filesMap.Set(file, pkg)
		}
	}
	if cache, ok := q.filesMap.LookUp(searchFile); ok {
		return cache, nil
	}
	return nil, nil
}

func (d *query) Load() error {
	statusPath := rootfs.NewPath(path.Join(DefaultBaseDir, "status"))
	content, err := fs.ReadFile(d.fs, statusPath.Path())
	if err != nil {
		return err
	}
	pkgs, err := ParseStatus(string(content))
	if err != nil {
		return err
	}
	d.filesMap.Clear()
	d.packages = append([]*pkg{}, pkgs...)
	d.nameMap.Clear()
	for _, pkg := range pkgs {
		pkg.fs = d.fs
		name := pkg.Name()
		arch := pkg.Architecture()
		// forcely add arch to identify the package
		nameWithArch := fmt.Sprintf("%s:%s", name, arch)
		d.nameMap.Set(name, append(d.nameMap.Get(name), pkg))
		d.nameMap.Set(nameWithArch, append(d.nameMap.Get(nameWithArch), pkg))
	}
	return nil
}
