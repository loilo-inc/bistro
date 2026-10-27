package bistro

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type Finder interface {
	PackagesForExecutable(file string) ([]*dpkg.Package, error)
}

type finder struct {
	dpkg dpkg.Dpkg
	ldd  ldd.Ldd
}

func CreateFinder(dpkg dpkg.Dpkg, ldd ldd.Ldd) Finder {
	return &finder{dpkg: dpkg, ldd: ldd}
}

var _ Finder = &finder{}

func (b *finder) PackagesForExecutable(file string) ([]*dpkg.Package, error) {
	if !filepath.IsAbs(file) {
		return nil, errors.New("file path is not absolute: " + file)
	}
	info, err := b.ldd.ListObjects(file)
	if err != nil {
		return nil, err
	}
	var visitedPackages = make(map[string]struct{})
	var packages []*dpkg.Package
	for _, lib := range info.Libs {
		pkg, err := b.CollectPackage(lib.Path)
		if err != nil {
			return nil, err
		}
		if _, ok := visitedPackages[pkg.Package]; !ok {
			visitedPackages[pkg.Package] = struct{}{}
			packages = append(packages, pkg)
		}
	}
	return packages, nil
}

func (b *finder) CollectPackage(libfile string) (*dpkg.Package, error) {
	realpath, err := realpath(libfile)
	if err != nil {
		return nil, err
	}
	result, err := b.dpkg.Search(realpath)
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
