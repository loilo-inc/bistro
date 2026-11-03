package bistro

import (
	"errors"
	"path"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Finder interface {
	TraceDepsForExecutable(file string) (*ExeTraceResult, error)
	FindPackageForExecutable(file string) (dpkg.Package, error)
}

type finder struct {
	DI
}

type ExeDependency struct {
	Library *ldd.Library
	Package dpkg.Package
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
	if !path.IsAbs(file) {
		return nil, errors.New("file path is not absolute: " + file)
	}

	var result ExeTraceResult

	rootLibs, err := f.DI.Ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}

	uniqueLibs, broken := ldd.FlattenDeps(rootLibs...)
	result.BrokenLibs = append(result.BrokenLibs, broken...)

	for _, lib := range uniqueLibs {
		realpath, err := f.SymLink.Resolve(lib.Path)
		if err != nil {
			return nil, err
		}
		pkg, err := f.searchPackage(realpath.Abs())
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

func (f *finder) FindPackageForExecutable(file string) (dpkg.Package, error) {
	if !path.IsAbs(file) {
		return nil, errors.New("file path is not absolute: " + file)
	}
	realpath, err := f.SymLink.Resolve(rootfs.NewPath(file))
	if err != nil {
		return nil, err
	}
	return f.searchPackage(realpath.Abs())
}

var errPackageNotFound = errors.New("package not found")

func (f *finder) searchPackage(file string) (dpkg.Package, error) {
	result, err := f.DI.Query.LookUp(file)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errPackageNotFound
	}
	return result, nil
}
