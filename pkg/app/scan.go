package app

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type scanApp struct {
	*app
	bistro.DI
	scanAppInput
}

type scanAppInput struct {
	paths    []rootfs.Path
	excludes []rootfs.Path
	format   string
	fs       fs.FS
}

func (c *app) newScanApp(input scanAppInput) *scanApp {
	return &scanApp{
		app:          c,
		DI:           c.depsFromFs(input.fs),
		scanAppInput: input,
	}
}

func (c *scanApp) Run(ctx context.Context) error {
	if err := load(c.DI); err != nil {
		return err
	}
	switch c.format {
	case "list":
		return c.scanList(c.paths, c.excludes, false)
	case "flat":
		return c.scanList(c.paths, c.excludes, true)
	case "tree":
		return c.scanTree(c.paths, c.excludes)
	}
	return fmt.Errorf("unknown format: %s", c.format)
}

func (c *scanApp) scan(paths []rootfs.Path, excludes []rootfs.Path) ([]*bistro.ElfDependency, error) {
	scanner := bistro.NewScanner(c.DI)
	scanner.SetExcludes(rootfs.NewPathSet(excludes))
	var elfDeps []*bistro.ElfDependency
	for _, path := range paths {
		deps, err := scanner.Scan(path)
		if err != nil {
			return nil, err
		}
		elfDeps = append(elfDeps, deps...)
	}
	return elfDeps, nil
}

func (c *scanApp) scanList(paths []rootfs.Path, excludes []rootfs.Path, flat bool) error {
	list, err := c.scan(paths, excludes)
	if err != nil {
		return err
	}
	pkgs := util.Map[string, *dpkg.Package]{}
	libs := util.Map[string, *ldd.Library]{}
	joinDeps := func() string {
		var deps []string
		for _, pkg := range pkgs.Iter() {
			deps = append(deps, pkg.NameWithArch())
		}
		for _, lib := range libs.Iter() {
			deps = append(deps, lib.Path.Abs())
		}
		return strings.Join(deps, " ")
	}
	for _, elfDep := range list {
		for _, exeDep := range elfDep.Dependencies {
			if exeDep.Package != nil {
				pkgs.Set(exeDep.Package.NameWithArch(), exeDep.Package)
			} else {
				libs.Set(exeDep.Library.Path.Abs(), exeDep.Library)
			}
		}
		for _, broken := range elfDep.BrokenLibs {
			c.errorf("[BROKEN] %s\n", broken)
		}
		var deps []string
		for _, pkg := range pkgs.Iter() {
			deps = append(deps, pkg.NameWithArch())
		}
		for _, lib := range libs.Iter() {
			deps = append(deps, lib.Path.Abs())
		}
		if !flat {
			c.printf("%s: %s\n", elfDep.ElfFile.Path, joinDeps())
			pkgs.Clear()
			libs.Clear()
		}
	}
	if flat {
		c.printf("%s\n", joinDeps())
	}
	return nil
}

func (c *scanApp) scanTree(paths []rootfs.Path, excludes []rootfs.Path) error {
	elfDeps, err := c.scan(paths, excludes)
	if err != nil {
		return err
	}
	for _, elfDep := range elfDeps {
		c.printf("%s\n", elfDep.ElfFile.Path)
		for _, exeDep := range elfDep.Dependencies {
			pkgName := "<none>"
			if exeDep.Package != nil {
				pkgName = exeDep.Package.NameWithArch()
			}
			c.printf("  %s => %s\n", exeDep.Library.Path, pkgName)
		}
		for _, broken := range elfDep.BrokenLibs {
			c.errorf("  [BROKEN] %s\n", broken)
		}
	}
	return nil
}
