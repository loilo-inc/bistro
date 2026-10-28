package bistro

import (
	"errors"
	"os"
	"path/filepath"
)

type Baker interface {
	CreateRootFS(dstDir string, dpkgList []*Package) error
}

type baker struct {
	dpkg Dpkg
	ldd  Ldd
}

var _ Baker = &baker{}

func CreateBaker(dpkg Dpkg, ldd Ldd) Baker {
	return &baker{dpkg: dpkg, ldd: ldd}
}

func (b *baker) CreateRootFS(out string, dpkgList []*Package) error {
	tmpFile, err := os.CreateTemp("", "rootfs")
	if err != nil {
		return err
	}
	rootfs := NewRootFS(tmpFile)
	defer rootfs.Close()

	for _, pkg := range dpkgList {
		if err := b.writePackageMetadata(rootfs, pkg); err != nil {
			return err
		}
	}

	if err := os.Rename(tmpFile.Name(), out); err != nil {
		return err
	}

	return nil
}

// https://github.com/GoogleContainerTools/distroless/blob/main/PACKAGE_METADATA.md
// directory to store dpkg status used by distroless
const DpkgStatusDir = "/var/lib/dpkg/status.d"

func (b *baker) writePackageMetadata(rootFS RootFS, pkg *Package) error {
	status, err := b.dpkg.PackageStatus(pkg.Package)
	if err != nil {
		return errors.New("failed to get status for package:" + pkg.Package)
	}
	if err = rootFS.Add(filepath.Join(DpkgStatusDir, pkg.Package), status, 0644); err != nil {
		return err
	}
	md5sums, err := b.dpkg.PackageMd5Sums(pkg.Package)
	if err != nil {
		return errors.New("failed to get md5sums for package:" + pkg.Package)
	}
	if err = rootFS.Add(filepath.Join(DpkgStatusDir, pkg.Package+".md5sums"), md5sums, 0644); err != nil {
		return err
	}
	files, err := b.dpkg.ListFiles(pkg.Package)
	if err != nil {
		return errors.New("failed to search files for package:" + pkg.Package)
	}

	for _, srcPath := range files {
		if err := rootFS.Copy(srcPath); err != nil {
			return err
		}
	}
	return nil
}
