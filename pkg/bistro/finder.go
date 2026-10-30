package bistro

import (
	"errors"
	"os"
	"path/filepath"
)

type Finder interface {
	TraceDepsForExecutable(file string) (*ExeTraceResult, error)
	FindPackageForExecutable(file string) (*Package, error)
}

type finder struct {
	dpkg        Dpkg
	ldd         Ldd
	lddCache    map[string]*LinkInfo
	pkgCache    map[string]*Package
	searchCache map[string]*Package
}

type ExeDependency struct {
	Library *Library
	Package *Package
}

func NewFinder(dpkg Dpkg, ldd Ldd) Finder {
	return &finder{dpkg: dpkg, ldd: ldd,
		lddCache:    map[string]*LinkInfo{},
		pkgCache:    map[string]*Package{},
		searchCache: map[string]*Package{},
	}
}

type ExeTraceResult struct {
	Dependencies []*ExeDependency
	BrokenLibs   []string
}

var _ Finder = &finder{}

func (b *finder) TraceDepsForExecutable(file string) (*ExeTraceResult, error) {
	if !filepath.IsAbs(file) {
		return nil, errors.New("file path is not absolute: " + file)
	}

	var result ExeTraceResult

	info, err := b.listLibs(file)
	if err != nil {
		return nil, err
	}

	for _, lib := range info.Libs {
		if lib.IsBroken() {
			result.BrokenLibs = append(result.BrokenLibs, lib.Name)
			continue
		}
		realpath, err := realpath(lib.Path)
		if err != nil {
			return nil, err
		}
		pkg, err := b.searchPackage(realpath)
		if err != nil && err != errPackageNotFound {
			return nil, err
		}
		result.Dependencies = append(result.Dependencies, &ExeDependency{
			Library: lib,
			Package: pkg,
		})
	}
	return &result, nil
}

func (b *finder) FindPackageForExecutable(file string) (*Package, error) {
	if !filepath.IsAbs(file) {
		return nil, errors.New("file path is not absolute: " + file)
	}
	realpath, err := realpath(file)
	if err != nil {
		return nil, err
	}
	return b.searchPackage(realpath)
}

func (f *finder) listLibs(file string) (*LinkInfo, error) {
	if cache, ok := f.lddCache[file]; ok {
		return cache, nil
	}
	info, err := f.ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}
	f.lddCache[file] = info
	return info, nil
}

var errPackageNotFound = errors.New("package not found")

func (f *finder) searchPackage(file string) (*Package, error) {
	if cache, ok := f.searchCache[file]; ok {
		return cache, nil
	}
	result, err := f.dpkg.Search(file)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errPackageNotFound
	}
	pkg, err := f.getPackage(result[0].Package)
	if err != nil {
		return nil, err
	}
	f.searchCache[file] = pkg
	return pkg, nil
}

func (f *finder) getPackage(packageName string) (*Package, error) {
	if cache, ok := f.pkgCache[packageName]; ok {
		return cache, nil
	}
	pkgs, err := f.dpkg.ListPackages(packageName)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, errors.New("package not found: " + packageName)
	}
	f.pkgCache[packageName] = pkgs[0]
	return pkgs[0], nil
}

func realpath(file string) (string, error) {
	if !filepath.IsAbs(file) {
		return "", errors.New("library path is not absolute: " + file)
	}
	stat, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if stat.IsDir() {
		return "", errors.New("library path is a directory: " + file)
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	return resolved, nil
}
