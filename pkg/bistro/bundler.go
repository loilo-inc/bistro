package bistro

import (
	"fmt"
	"strings"
)

type BundleInput struct {
	Out      string
	Files    []string
	Scans    []string
	Packages []string
}

type Bundler interface {
	Bundle(input BundleInput) error
}

type bundler struct {
	dpkg            Dpkg
	ldd             Ldd
	scanner         Scanner
	packages        []*Package
	files           []string
	missingPackages []string
	brokenDeps      []*ElfDependency
	visitedPackages map[string]struct{}
}

func NewBundler(dpkg Dpkg, ldd Ldd) *bundler {
	b := &bundler{dpkg: dpkg, ldd: ldd}
	b.reset()
	return b
}

func (b *bundler) reset() {
	b.packages = nil
	b.files = nil
	b.missingPackages = nil
	b.brokenDeps = nil
	b.scanner = NewScanner(b.ldd, b.dpkg)
	b.visitedPackages = make(map[string]struct{})
}

func (b *bundler) Bundle(input BundleInput) error {
	baker := NewBaker(b.dpkg, b.ldd)

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

	if len(input.Packages) > 0 {
		pkgs, err := b.dpkg.ListPackages(input.Packages...)
		if err != nil {
			return err
		}
		for _, pkg := range pkgs {
			b.addPackage(pkg)
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

	return baker.CreateRootFS(&BakeInput{
		Out:      input.Out,
		DpkgList: b.packages,
		Files:    b.files,
	})
}

func (b *bundler) addDependency(deps ...*ElfDependency) {
	for _, dep := range deps {
		if len(dep.BrokenLibs) > 0 {
			b.brokenDeps = append(b.brokenDeps, dep)
		}
		for _, exeDep := range dep.Dependencies {
			b.addPackage(exeDep.Package)
		}
	}
}

func (b *bundler) addPackage(target ...*Package) {
	for _, t := range target {
		if _, ok := b.visitedPackages[t.Package]; !ok {
			b.visitedPackages[t.Package] = struct{}{}
			b.packages = append(b.packages, t)
			if t.Status != PackageStatusInstalled {
				b.missingPackages = append(b.missingPackages, t.Package)
			}
		}
	}
}
