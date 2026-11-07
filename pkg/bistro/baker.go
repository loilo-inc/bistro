package bistro

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Baker interface {
	CreateRootFS(input *BakeInput) error
}

type baker struct {
	DI
}

var _ Baker = &baker{}

func NewBaker(di DI) Baker {
	return &baker{DI: di}
}

type BakeInput struct {
	Out      string
	DpkgList []*dpkg.Package
	Files    []rootfs.Path
	Excludes rootfs.PathSet
}

func (b *baker) CreateRootFS(input *BakeInput) (err error) {
	// Distroless metadata filenames use the unqualified package name.
	// Reject collisions before creating an archive that would lose metadata.
	names := make(map[string]string, len(input.DpkgList))
	for _, pkg := range input.DpkgList {
		if previous, ok := names[pkg.Name()]; ok {
			return fmt.Errorf("package metadata name conflict: %s and %s use %s", previous, pkg.NameWithArch(), pkg.Name())
		}
		names[pkg.Name()] = pkg.NameWithArch()
	}
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

	archiver := rootfs.NewArchiver(b.FS, tmpFile)
	archiver.SetExcludes(input.Excludes)

	for _, pkg := range input.DpkgList {
		if err := b.writePackageMetadata(archiver, pkg, input.Excludes); err != nil {
			return err
		}
	}
	for _, file := range input.Files {
		if err := archiver.CopyRecursive(file); err != nil {
			return err
		}
	}
	if err := archiver.Close(); err != nil {
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

func (b *baker) writePackageMetadata(
	rootFS rootfs.Archiver,
	pkg *dpkg.Package,
	excludes rootfs.PathSet,
) error {
	if err := rootFS.Add(rootfs.MustPath(DpkgStatusDir).Join(pkg.Name()), []byte(pkg.String()), 0644); err != nil {
		return err
	}
	md5sums, err := pkg.Md5Sums(b.FS)
	if err != nil {
		return err
	}
	if err = rootFS.Add(rootfs.MustPath(DpkgStatusDir).Join(pkg.Name()+".md5sums"), []byte(md5sums.String()), 0644); err != nil {
		return err
	}
	files, err := pkg.ListFiles(b.FS)
	if err != nil {
		return err
	}

	for _, srcPath := range files {
		if skip, err := excludes.Match(b.FS, srcPath); err != nil {
			return err
		} else if skip {
			continue
		}
		if err := rootFS.Copy(srcPath); os.IsNotExist(err) {
			b.Logger.Error("File does not exist: %s", srcPath)
			continue
		} else if err != nil {
			return err
		}
	}
	return nil
}
