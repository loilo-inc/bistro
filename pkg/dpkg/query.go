package dpkg

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"runtime"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Query interface {
	Get(name string) (*Package, error)
	List() ([]*Package, error)
	Load() error
	LookUp(file string) (*Package, error)
}

type query struct {
	// must be arm64/amd64
	arch     string
	packages []*Package
	nameMap  map[string][]*Package
	filesMap map[string]*Package
	fs       fs.FS
}

var _ Query = &query{}

type OwnedFile struct {
	Package *Package
	File    string
}

var dpkgDir = rootfs.MustPath("/var/lib/dpkg")
var dpkgStatusFile = dpkgDir.Join("status")
var dpkgInfoDir = dpkgDir.Join("info")

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
	return &query{fs: fs, arch: runtime.GOARCH}
}

func (d *query) Get(name string) (*Package, error) {
	if d.nameMap == nil {
		return nil, errors.New("packages not loaded")
	}
	list, ok := d.nameMap[name]
	if !ok {
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

func (d *query) List() ([]*Package, error) {
	if d.nameMap == nil {
		return nil, errors.New("packages not loaded")
	}
	return d.packages, nil
}

func (q *query) LookUp(searchFile string) (*Package, error) {
	if !path.IsAbs(searchFile) {
		return nil, fmt.Errorf("pattern must be an absolute path: %s", searchFile)
	}
	if q.filesMap != nil {
		return q.filesMap[searchFile], nil
	}
	packages, err := q.List()
	if err != nil {
		return nil, err
	}
	var filesMap = make(map[string]*Package)
	for _, pkg := range packages {
		files, err := pkg.ListFiles(q.fs)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			filesMap[file.Abs()] = pkg
		}
	}
	q.filesMap = filesMap
	return filesMap[searchFile], nil
}

func (d *query) Load() error {
	content, err := fs.ReadFile(d.fs, dpkgStatusFile.Path())
	if err != nil {
		return err
	}
	pkgs, err := ParseStatus(string(content))
	if err != nil {
		return err
	}
	d.filesMap = nil
	d.packages = pkgs
	d.nameMap = make(map[string][]*Package)
	for _, pkg := range pkgs {
		name := pkg.Name()
		arch := pkg.Architecture()
		// forcely add arch to identify the package
		nameWithArch := fmt.Sprintf("%s:%s", name, arch)
		d.nameMap[name] = append(d.nameMap[name], pkg)
		d.nameMap[nameWithArch] = append(d.nameMap[nameWithArch], pkg)
	}
	return nil
}
