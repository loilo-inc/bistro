package bistro

import (
	"strings"
)

type Dpkg interface {
	ListPackages(packages ...[]string) ([]*Package, error)
	ListFiles(pkgName string) ([]string, error)
	Search(pattern string) ([]*OwnedFile, error)
	PackageStatus(pkgName string) ([]byte, error)
	PackageMd5Sums(pkgName string) ([]byte, error)
}

type dpkg struct {
	execer Execer
}

var _ Dpkg = &dpkg{}

func CreateDpkg(execer Execer) Dpkg {
	return &dpkg{execer: execer}
}

type Package struct {
	Package      string `json:"package"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
}

type OwnedFile struct {
	Package string
	File    string
}

func (b *dpkg) ListPackages(packages ...[]string) (dpkgList []*Package, err error) {
	args := []string{"-W", "-f=${binary:Package}\t${Version}\t${Architecture}\n"}
	for _, pkgGroup := range packages {
		args = append(args, pkgGroup...)
	}
	out, err := b.execer.Output("dpkg-query", args...)
	if err != nil {
		return nil, err
	}
	tsv := parseTabSplitData(out)
	dpkgList = make([]*Package, 0, len(tsv))
	for _, row := range tsv {
		if len(row) < 3 {
			continue
		}
		dpkgList = append(dpkgList, &Package{
			Package:      row[0],
			Version:      row[1],
			Architecture: row[2],
		})
	}
	return dpkgList, nil
}

func (b *dpkg) ListFiles(pkgName string) (files []string, err error) {
	out, err := b.execer.Output("dpkg-query", "-L", pkgName)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitSeq(string(out), "\n")
	for line := range lines {
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

func (b *dpkg) PackageStatus(pkgName string) ([]byte, error) {
	return b.execer.Output("dpkg-query", "-s", pkgName)
}

func (b *dpkg) PackageMd5Sums(pkgName string) ([]byte, error) {
	return b.execer.Output("dpkg-query", "--control-show", pkgName, "md5sums")
}

func (b *dpkg) Search(pattern string) (packages []*OwnedFile, err error) {
	out, err := b.execer.Output("dpkg-query", "-S", pattern)
	if err != nil {
		return nil, err
	}
	return parseSearchData(out), nil
}

func parseTabSplitData(data []byte) [][]string {
	lines := strings.Split(string(data), "\n")
	result := make([][]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		result = append(result, strings.Split(line, "\t"))
	}
	return result
}

func parseSearchData(data []byte) []*OwnedFile {
	// Ex) libc6:arm64: /usr/lib/aarch64-linux-gnu/gconv/GB18030.so
	lines := strings.Split(string(data), "\n")
	result := make([]*OwnedFile, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		lib := strings.TrimSuffix(parts[0], ":")
		path := strings.TrimSpace(parts[1])
		result = append(result, &OwnedFile{
			Package: lib,
			File:    path,
		})
	}
	return result
}

type PackageDiff struct {
	Added   []*Package
	Removed []*Package
	Changed []*Package
}

func DiffPackages(before []*Package, after []*Package) (
	diff PackageDiff, err error,
) {
	beforeList := before
	afterList := after
	var added []*Package
	var removed []*Package
	var changed []*Package
	beforeMap := make(map[string]*Package)
	for _, pkg := range beforeList {
		beforeMap[pkg.Package] = pkg
	}
	afterMap := make(map[string]*Package)
	for _, pkg := range afterList {
		afterMap[pkg.Package] = pkg
	}
	for pkgName, pkg := range afterMap {
		if _, ok := beforeMap[pkgName]; !ok {
			added = append(added, pkg)
		}
	}
	for pkgName, pkg := range beforeMap {
		if _, ok := afterMap[pkgName]; !ok {
			removed = append(removed, pkg)
		}
	}
	for pkgName, pkg := range afterMap {
		if beforePkg, ok := beforeMap[pkgName]; ok {
			if beforePkg.Version != pkg.Version {
				changed = append(changed, pkg)
			}
		}
	}
	return PackageDiff{
		Added:   added,
		Removed: removed,
		Changed: changed,
	}, nil
}
