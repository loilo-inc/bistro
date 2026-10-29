package bistro

import (
	"os"
	"path/filepath"
)

type Baker interface {
	CreateRootFS(input *BakeInput) error
}

type baker struct {
	dpkg Dpkg
	ldd  Ldd
}

var _ Baker = &baker{}

func NewBaker(dpkg Dpkg, ldd Ldd) Baker {
	return &baker{dpkg: dpkg, ldd: ldd}
}

type BakeInput struct {
	Out      string
	DpkgList []*Package
	Files    []string
}

func (b *baker) CreateRootFS(input *BakeInput) (err error) {
	tmpFile, err := os.CreateTemp(filepath.Dir(input.Out), "rootfs")
	if err != nil {
		return err
	}
	defer tmpFile.Close()
	defer func() {
		if err != nil {
			os.Remove(tmpFile.Name())
		}
	}()
	rootfs := NewRootFS(tmpFile)
	for _, pkg := range input.DpkgList {
		if err := b.writePackageMetadata(rootfs, pkg); err != nil {
			return err
		}
	}
	for _, file := range input.Files {
		if err := rootfs.Copy(file); err != nil {
			return err
		}
	}
	if err := rootfs.Close(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpFile.Name(), input.Out); err != nil {
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
		return err
	}
	if err = rootFS.Add(filepath.Join(DpkgStatusDir, pkg.Package), status, 0644); err != nil {
		return err
	}
	md5sums, err := b.dpkg.PackageMd5Sums(pkg.Package)
	if err != nil {
		return err
	}
	if err = rootFS.Add(filepath.Join(DpkgStatusDir, pkg.Package+".md5sums"), md5sums, 0644); err != nil {
		return err
	}
	files, err := b.dpkg.ListFiles(pkg.Package)
	if err != nil {
		return err
	}

	for _, srcPath := range files {
		if err := rootFS.Copy(srcPath); err != nil {
			return err
		}
	}
	return nil
}
