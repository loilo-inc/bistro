package bistro

import (
	"fmt"
	"strings"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type BundleInput struct {
	Out          rootfs.Path
	Files        []rootfs.Path
	Packages     []string
	NoScan       bool
	Excludes     []rootfs.Path
	ExcludeScans []rootfs.Path
}

type bundler struct {
	DI
	BundleInput
	scanner         Scanner
	baker           Baker
	packages        []*dpkg.Package
	excludes        rootfs.PathSet
	files           []rootfs.Path
	missingPackages []string
	brokenDeps      []*ElfDependency
	visitedPackages util.Map[string, bool]
}

func Bundle(di DI, input BundleInput) error {
	excludes := rootfs.NewPathSet(input.Excludes)
	// special files to always exclude
	excludes.Add(rootfs.MustPath("/sys"), rootfs.MustPath("/proc"))
	if err := excludes.Resolve(di.FS); err != nil {
		return err
	}
	excludeScans := excludes.Clone()
	excludeScans.Add(input.ExcludeScans...)
	if err := excludeScans.Resolve(di.FS); err != nil {
		return err
	}
	scanner := NewScanner(di)
	scanner.SetExcludes(excludeScans)
	baker := NewBaker(di)
	b := &bundler{
		DI:          di,
		BundleInput: input,
		scanner:     scanner,
		baker:       baker,
		excludes:    excludes,
	}
	return b.bundle()
}

func (b *bundler) bundle() error {
	for _, file := range b.Files {
		if err := b.addFile(file); err != nil {
			return err
		}
		if b.NoScan {
			continue
		}
		deps, err := b.scanner.Scan(file)
		if err != nil {
			return err
		}
		if err := b.addDependency(deps); err != nil {
			return err
		}
		// Add dependent files to the list of files for bundling target
		for _, dep := range deps {
			if err := b.addFile(dep.ElfFile.Path); err != nil {
				return err
			}
		}
	}

	for _, packageName := range b.Packages {
		pkg, err := b.DI.Query.Get(packageName)
		if err != nil {
			return err
		}
		b.addPackage(pkg)
	}

	if len(b.brokenDeps) > 0 {
		dep := b.brokenDeps[0]
		return fmt.Errorf("some libraries are not linked: %s => %v", dep.ElfFile.Path, dep.BrokenLibs)
	}

	if len(b.missingPackages) > 0 {
		str := strings.Join(b.missingPackages, ", ")
		return fmt.Errorf("depended packages are not installed: %v", str)
	}

	return b.baker.CreateRootFS(&BakeInput{
		Out:      b.Out,
		DpkgList: b.packages,
		Files:    b.files,
		Excludes: b.excludes,
	})
}

func (b *bundler) addDependency(deps []*ElfDependency) error {
	for _, dep := range deps {
		if len(dep.BrokenLibs) > 0 {
			b.brokenDeps = append(b.brokenDeps, dep)
		}
		if dep.Package != nil {
			b.addPackage(dep.Package)
		}
		for _, exeDep := range dep.Dependencies {
			if err := b.addFile(exeDep.Library.Path); err != nil {
				return err
			}
			if exeDep.Package != nil {
				b.addPackage(exeDep.Package)
			}
		}
	}
	return nil
}

func (b *bundler) addPackage(t *dpkg.Package) {
	if ok := b.visitedPackages.Has(t.NameWithArch()); !ok {
		b.visitedPackages.Set(t.NameWithArch(), true)
		b.packages = append(b.packages, t)
		if t.Status().Current != dpkg.StatusInstalled {
			b.missingPackages = append(b.missingPackages, t.NameWithArch())
		}
	}
}

func (b *bundler) addFile(file rootfs.Path) error {
	if matched, err := b.excludes.Match(b.FS, file); err != nil {
		return err
	} else if matched {
		return nil
	}
	b.files = append(b.files, file)
	return nil
}
