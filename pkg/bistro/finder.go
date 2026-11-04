package bistro

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Finder interface {
	TraceDepsForExecutable(file rootfs.Path) (*ExeTraceResult, error)
	FindPackageForExecutable(file rootfs.Path) (*dpkg.Package, error)
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

func (f *finder) TraceDepsForExecutable(file rootfs.Path) (*ExeTraceResult, error) {

	var result ExeTraceResult

	rootLibs, err := f.DI.Ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}

	uniqueLibs, broken := ldd.FlattenDeps(rootLibs...)
	result.BrokenLibs = append(result.BrokenLibs, broken...)

	for _, lib := range uniqueLibs {
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

func (b *finder) FindPackageForExecutable(file rootfs.Path) (*dpkg.Package, error) {
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

func realpath(file rootfs.Path) (string, error) {
	stat, err := os.Stat(file.Abs())
	if err != nil {
		return "", err
	}
	if stat.IsDir() {
		return "", errors.New("library path is a directory: " + file.Abs())
	}
	resolved, err := filepath.EvalSymlinks(file.Abs())
	if err != nil {
		return "", err
	}
	return resolved, nil
}
