package dpkg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Query interface {
	Get(name string) (*Package, error)
	List() ([]*Package, error)
	Load() error
	LookUp(file string) (*Package, error)
}

type query struct {
	baseDir string
	// must be arm64/amd64
	arch     string
	packages []*Package
	nameMap  map[string][]*Package
	filesMap map[string]*Package
}

var _ Query = &query{}

type OwnedFile struct {
	Package *Package
	File    string
}

const DefaultBaseDir = "/var/lib/dpkg"

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

func NewQuery() Query {
	return NewQueryWithBaseDir(DefaultBaseDir)
}

// NewQueryWithBaseDir reads the dpkg database from baseDir.
func NewQueryWithBaseDir(baseDir string) Query {
	return &query{baseDir: baseDir, arch: runtime.GOARCH}
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
	if !filepath.IsAbs(searchFile) {
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
		files, err := pkg.ListFiles()
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			filesMap[file] = pkg
		}
	}
	q.filesMap = filesMap
	return filesMap[searchFile], nil
}

func (d *query) Load() error {
	content, err := os.ReadFile(filepath.Join(d.baseDir, "status"))
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
		pkg.baseDir = d.baseDir
		name := pkg.Name()
		arch := pkg.Architecture()
		// forcely add arch to identify the package
		nameWithArch := fmt.Sprintf("%s:%s", name, arch)
		d.nameMap[name] = append(d.nameMap[name], pkg)
		d.nameMap[nameWithArch] = append(d.nameMap[nameWithArch], pkg)
	}
	return nil
}
