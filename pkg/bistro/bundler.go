package bistro

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type BundleInput struct {
	Out      string
	Files    []string
	Scans    []string
	Packages []string
	Copies   []string
}

type Bundler interface {
	Bundle(input BundleInput) error
}

type bundler struct {
	DI
	scanner         Scanner
	packages        []dpkg.Package
	files           []rootfs.Path
	missingPackages []string
	brokenDeps      []*ElfDependency
	visitedPackages map[string]struct{}
}

func NewBundler(di DI) *bundler {
	b := &bundler{DI: di}
	b.reset()
	return b
}

func (b *bundler) reset() {
	b.packages = nil
	b.files = nil
	b.missingPackages = nil
	b.brokenDeps = nil
	b.scanner = NewScanner(b.DI)
	b.visitedPackages = make(map[string]struct{})
}

func (b *bundler) Bundle(input BundleInput) error {
	baker := NewBaker(b.DI)

	defer b.reset()

	for _, file := range input.Files {
		deps, err := b.scanner.Scan(file)
		if err != nil {
			return err
		}
		b.addDependency(deps...)
		// Add dependent files to the list of files for bundling target
		for _, dep := range deps {
			b.files = append(b.files, dep.ElfFile.Path)
		}
	}

	for _, scan := range input.Scans {
		deps, err := b.scanner.Scan(scan)
		if err != nil {
			return err
		}
		b.addDependency(deps...)
		// Ignore dependent files from scans, as they will be copied
	}

	for _, packageName := range input.Packages {
		pkg, err := b.DI.Query.Get(packageName)
		if err != nil {
			return err
		}
		b.addPackage(pkg)
	}

	for _, copy := range input.Copies {
		if !path.IsAbs(copy) {
			return fmt.Errorf("path must be absolute: %s", copy)
		}
		rootPath := rootfs.NewPath(copy)
		if err := b.addCopyFile(rootPath); err != nil {
			return err
		}
	}

	if len(b.brokenDeps) > 0 {
		dep := b.brokenDeps[0]
		return fmt.Errorf("some libraries are not linked: %s => %v", dep.ElfFile.Path, dep.BrokenLibs)
	}

	if len(b.missingPackages) > 0 {
		str := strings.Join(b.missingPackages, ", ")
		return fmt.Errorf("depended packages are not installed: %v", str)
	}
	var absFiles []string
	for _, file := range b.files {
		absFiles = append(absFiles, file.Abs())
	}
	return baker.CreateRootFS(BakeInput{
		Out:      input.Out,
		DpkgList: b.packages,
		Files:    absFiles,
	})
}

func (b *bundler) addCopyFile(path rootfs.Path) error {
	lstat, err := fs.Lstat(b.FS, path.Path())
	if err != nil {
		return err
	}

	b.files = append(b.files, path)

	if !lstat.IsDir() {
		return nil
	}
	entries, err := fs.ReadDir(b.FS, path.Path())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		err := b.addCopyFile(path.Join(entry.Name()))
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *bundler) addDependency(deps ...*ElfDependency) {
	for _, dep := range deps {
		if len(dep.BrokenLibs) > 0 {
			b.brokenDeps = append(b.brokenDeps, dep)
		}
		if dep.Package != nil {
			b.addPackage(dep.Package)
		}
		for _, exeDep := range dep.Dependencies {
			if exeDep.Package != nil {
				b.addPackage(exeDep.Package)
			}
		}
	}
}

func (b *bundler) addPackage(target ...dpkg.Package) {
	for _, t := range target {
		if _, ok := b.visitedPackages[t.NameWithArch()]; !ok {
			b.visitedPackages[t.NameWithArch()] = struct{}{}
			b.packages = append(b.packages, t)
			if t.Status().Current != dpkg.StatusInstalled {
				b.missingPackages = append(b.missingPackages, t.NameWithArch())
			}
		}
	}
}
