package bistro

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type Baker interface {
	CreateRootFS(dstDir string, dpkgList []*dpkg.Package) error
}

type baker struct {
	dpkg dpkg.Dpkg
	ldd  ldd.Ldd
}

var _ Baker = &baker{}

func CreateBaker(dpkg dpkg.Dpkg, ldd ldd.Ldd) Baker {
	return &baker{dpkg: dpkg, ldd: ldd}
}

func (b *baker) CreateRootFS(dstDir string, dpkgList []*dpkg.Package) error {
	err := os.MkdirAll(filepath.Join(dstDir), 0755)
	if err != nil {
		return err
	}
	for _, pkg := range dpkgList {
		if err := b.WritePackageMetadata(dstDir, pkg); err != nil {
			return err
		}
	}
	return nil
}

// https://github.com/GoogleContainerTools/distroless/blob/main/PACKAGE_METADATA.md
// directory to store dpkg status used by distroless
const DpkgStatusDir = "/var/lib/dpkg/status.d"

func (b *baker) WritePackageMetadata(root string, pkg *dpkg.Package) error {
	err := os.MkdirAll(filepath.Join(root, DpkgStatusDir), 0755)
	if err != nil {
		return err
	}
	status, err := b.dpkg.PackageStatus(pkg.Package)
	if err != nil {
		return errors.New("failed to get status for package:" + pkg.Package)
	}
	if err = os.WriteFile(filepath.Join(root, DpkgStatusDir, pkg.Package), status, 0644); err != nil {
		return err
	}
	md5sums, err := b.dpkg.PackageMd5Sums(pkg.Package)
	if err != nil {
		return errors.New("failed to get md5sums for package:" + pkg.Package)
	}
	if err = os.WriteFile(filepath.Join(root, DpkgStatusDir, pkg.Package+".md5sums"), md5sums, 0644); err != nil {
		return err
	}
	files, err := b.dpkg.ListFiles(pkg.Package)
	if err != nil {
		return errors.New("failed to search files for package:" + pkg.Package)
	}

	for _, srcPath := range files {
		if err := copyFile(srcPath, root); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(srcPath string, dstRoot string) error {
	dstPath := filepath.Join(dstRoot, srcPath)
	stat, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return os.MkdirAll(dstPath, 0755)
	}
	err = os.MkdirAll(filepath.Dir(dstPath), 0755)
	if err != nil {
		return err
	}
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	dstFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}
	return nil
}
