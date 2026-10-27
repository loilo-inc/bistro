package dpkg

import (
	"strings"

	"github.com/loilo-inc/bistro/pkg/execer"
)

type Dpkg interface {
	ListPackages(packages ...[]string) ([]*Package, error)
	ListFiles(pkgName string) ([]string, error)
	Search(pattern string) ([]*OwnedFile, error)
	PackageStatus(pkgName string) ([]byte, error)
	PackageMd5Sums(pkgName string) ([]byte, error)
}

type dpkg struct {
	execer execer.Execer
}

var _ Dpkg = &dpkg{}

func CreateDpkg(execer execer.Execer) Dpkg {
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
	args := []string{"-W", "-f=${Package}\t${Version}\t${Architecture}\n"}
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
	lines := parseColonSplitData(out)
	packages = make([]*OwnedFile, 0, len(lines))
	for _, row := range lines {
		if len(row) < 2 {
			continue
		}
		packageName := strings.TrimSpace(row[0])
		file := strings.TrimSpace(row[1])
		packages = append(packages, &OwnedFile{
			Package: packageName,
			File:    file,
		})
	}
	return packages, nil
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

func parseColonSplitData(data []byte) [][]string {
	lines := strings.Split(string(data), "\n")
	result := make([][]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		result = append(result, strings.SplitN(line, ":", 2))
	}
	return result
}
