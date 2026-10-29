package bistro

import (
	"errors"
	"os"
	"path/filepath"
)

type Finder interface {
	TraceDepsForExecutable(file string) (*ExeTraceResult, error)
}

type finder struct {
	dpkg Dpkg
	ldd  Ldd
}

type ExeDependency struct {
	Library *Library
	Package *Package
}

func CreateFinder(dpkg Dpkg, ldd Ldd) Finder {
	return &finder{dpkg: dpkg, ldd: ldd}
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
	info, err := b.ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}

	var result ExeTraceResult
	for _, lib := range info.Libs {
		if lib.IsBroken() {
			result.BrokenLibs = append(result.BrokenLibs, lib.Name)
			continue
		}
		pkg, err := b.CollectPackage(lib.Path)
		if err != nil {
			return nil, err
		}
		result.Dependencies = append(result.Dependencies, &ExeDependency{
			Library: lib,
			Package: pkg,
		})
	}
	return &result, nil
}

func (b *finder) CollectPackage(libfile string) (*Package, error) {
	realpath, err := realpath(libfile)
	if err != nil {
		return nil, err
	}
	result, err := b.dpkg.Search(realpath)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("library not found in any package: " + realpath)
	}
	pkg, err := b.dpkg.ListPackages([]string{result[0].Package})
	if err != nil {
		return nil, err
	}
	if len(pkg) == 0 {
		return nil, errors.New("package not found: " + result[0].Package)
	}
	for _, p := range pkg {
		if p.Status != "installed" {
			return nil, errors.New("package is not installed: " + p.Package)
		}
	}
	return pkg[0], nil
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
