package bistro

import "strings"

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

func NewDpkg(execer Execer) Dpkg {
	return &dpkg{execer: execer}
}

type Package struct {
	Package      string `json:"package"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Status       string `json:"status"`
}

const PackageStatusInstalled = "installed"

type OwnedFile struct {
	Package string
	File    string
}

func (b *dpkg) ListPackages(packages ...[]string) (dpkgList []*Package, err error) {
	args := []string{"-W", "-f=${binary:Package}\t${Version}\t${Architecture}\t${db:Status-Status}\n"}
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
		if len(row) < 4 {
			continue
		}
		dpkgList = append(dpkgList, &Package{
			Package:      row[0],
			Version:      row[1],
			Architecture: row[2],
			Status:       row[3],
		})
	}
	return dpkgList, nil
}

func (b *dpkg) ListFiles(pkgName string) (files []string, err error) {
	out, err := b.execer.Output("dpkg-query", "-W", "-f=${db-fsys:Files}", pkgName)
	if err != nil {
		return nil, err
	}
	return parseLines(out), nil
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

func parseSearchData(data []byte) []*OwnedFile {
	var result []*OwnedFile
	for line := range strings.SplitSeq(string(data), "\n") {
		// DefaultExecer fixes the locale to C, so diversion descriptions are English.
		if strings.HasPrefix(line, "diversion by ") || strings.HasPrefix(line, "local diversion ") {
			continue
		}
		owners, path, ok := strings.Cut(line, ": ")
		if !ok || owners == "" || path == "" {
			continue
		}
		for _, owner := range strings.Split(owners, ", ") {
			result = append(result, &OwnedFile{Package: owner, File: path})
		}
	}
	return result
}
