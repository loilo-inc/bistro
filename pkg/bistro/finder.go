package bistro

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/dpkg"
)

type Finder interface {
	TraceDepsForExecutable(file string) (*ExeTraceResult, error)
	FindPackageForExecutable(file string) (*dpkg.Package, error)
}

type finder struct {
	DI
	lddCache    map[string]*LinkInfo
	searchCache map[string]*dpkg.Package
}

type ExeDependency struct {
	Library *Library
	Package *dpkg.Package
}

func NewFinder(di DI) Finder {
	return &finder{
		DI:          di,
		lddCache:    map[string]*LinkInfo{},
		searchCache: map[string]*dpkg.Package{},
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

func (b *finder) FindPackageForExecutable(file string) (*dpkg.Package, error) {
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
	info, err := f.DI.Ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}
	f.lddCache[file] = info
	return info, nil
}

var errPackageNotFound = errors.New("package not found")

func (f *finder) searchPackage(file string) (*dpkg.Package, error) {
	if cache, ok := f.searchCache[file]; ok {
		return cache, nil
	}
	result, err := f.DI.Query.Search(file)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errPackageNotFound
	}
	f.searchCache[file] = result[0].Package
	return result[0].Package, nil
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
