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
	// Note: Search is NOT compatible to dpkg-query -S.
	Search(pattern string) ([]*OwnedFile, error)
	Load() error
}

type query struct {
	baseDir string
	// must be arm64/amd64
	arch     string
	packages []*Package
	nameMap  map[string][]*Package
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

func (q *query) Search(pattern string) ([]*OwnedFile, error) {
	packages, err := q.List()
	if err != nil {
		return nil, err
	}
	var ownedFiles []*OwnedFile
	for _, pkg := range packages {
		files, err := pkg.ListFiles()
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if matched, _ := filepath.Match(pattern, file); matched {
				ownedFiles = append(ownedFiles, &OwnedFile{
					Package: pkg,
					File:    file,
				})
			}
		}
	}
	return ownedFiles, nil
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
