package bistro

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type Finder interface {
	TraceDepsForExecutable(file string) (*ExeTraceResult, error)
	FindPackageForExecutable(file string) (*dpkg.Package, error)
}

type finder struct {
	DI
}

type ExeDependency struct {
	Library *ldd.Library
	Package *dpkg.Package
}

func NewFinder(di DI) Finder {
	return &finder{DI: di}
}

type ExeTraceResult struct {
	Dependencies []*ExeDependency
	BrokenLibs   []string
}

var _ Finder = &finder{}

func (f *finder) TraceDepsForExecutable(file string) (*ExeTraceResult, error) {
	if !filepath.IsAbs(file) {
		return nil, errors.New("file path is not absolute: " + file)
	}

	var result ExeTraceResult

	rootLibs, err := f.DI.Ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}

	var uniqueLibs []*ldd.Library
	var visitedLibs = make(map[string]struct{})
	var queue []*ldd.Library
	queue = append(queue, rootLibs...)

	for len(queue) > 0 {
		lib := queue[0]
		queue = queue[1:]
		var libKey = lib.Path
		if libKey == "" {
			libKey = "[BROKEN]" + lib.Name
		}
		if _, ok := visitedLibs[libKey]; ok {
			continue
		}
		uniqueLibs = append(uniqueLibs, lib)
		visitedLibs[libKey] = struct{}{}
		queue = append(queue, lib.Deps...)
	}

	for _, lib := range uniqueLibs {
		if lib.IsBroken() {
			result.BrokenLibs = append(result.BrokenLibs, lib.Name)
			continue
		}
		realpath, err := realpath(lib.Path)
		if err != nil {
			return nil, err
		}
		pkg, err := f.searchPackage(realpath)
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

var errPackageNotFound = errors.New("package not found")

func (f *finder) searchPackage(file string) (*dpkg.Package, error) {
	result, err := f.DI.Query.LookUp(file)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errPackageNotFound
	}
	return result, nil
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
