package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
	"github.com/urfave/cli/v3"
)

type App interface {
	Run(ctx context.Context, args []string) error
}

type app struct {
	NewInput
}

var _ App = (*app)(nil)

type NewInput struct {
	Version string
	Commit  string
	Date    string
	Cwd     string
	DI      bistro.DI
	Stdout  io.Writer
	Stderr  io.Writer
}

func New(input NewInput) App {
	return &app{NewInput: input}
}

func (c *app) load() error {
	if err := c.DI.Query.Load(); err != nil {
		return err
	}
	if err := c.DI.Ldd.Load(); err != nil {
		return err
	}
	return nil
}

func (c *app) command() *cli.Command {
	return &cli.Command{
		Name:    "bistro",
		Usage:   "Build a distroless root filesystem",
		Version: fmt.Sprintf("%s (commit: %s, date: %s)", c.Version, c.Commit, c.Date),
		Commands: []*cli.Command{
			{
				Name:      "scan",
				ArgsUsage: "SCAN [fileorDirs...]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "format",
						Aliases: []string{"f"},
						Usage:   "Output format (list, flat, tree)",
						Value:   "tree",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return fmt.Errorf("scan requires at least one file or directory")
					}
					if err := c.load(); err != nil {
						return err
					}
					paths := c.rootfsPaths(cmd.Args().Slice()...)
					format := cmd.String("format")
					switch format {
					case "list":
						return c.scanList(false, paths...)
					case "flat":
						return c.scanList(true, paths...)
					case "tree":
						return c.scanTree(paths...)
					}
					return fmt.Errorf("unknown format: %s", format)
				},
			},
			{
				Name:      "bundle",
				ArgsUsage: "BUNDLE [executable...]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out",
						Aliases:  []string{"o"},
						Usage:    "Output root filesystem tar archive",
						Required: true,
					},
					&cli.StringSliceFlag{
						Name:  "scan",
						Usage: "Executable or directories to scan for dependencies",
					},
					&cli.StringSliceFlag{
						Name:  "package",
						Usage: "Packages to include in the root filesystem",
					},
					&cli.StringSliceFlag{
						Name:  "copy",
						Usage: "Files or directories to copy into the root filesystem",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := c.load(); err != nil {
						return err
					}
					return c.bundle(bistro.BundleInput{
						Out:      c.absPath(cmd.String("out")),
						Files:    c.rootfsPaths(cmd.Args().Slice()...),
						Scans:    c.rootfsPaths(cmd.StringSlice("scan")...),
						Packages: cmd.StringSlice("package"),
						Copies:   c.rootfsPaths(cmd.StringSlice("copy")...),
					})
				},
			},
		},
	}
}

func (c *app) Run(ctx context.Context, args []string) error {
	return c.command().Run(ctx, args)
}

func (c *app) scan(paths ...rootfs.Path) ([]*bistro.ElfDependency, error) {
	scanner := bistro.NewScanner(c.DI)
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

func (c *app) scanList(flat bool, paths ...rootfs.Path) error {
	list, err := c.scan(paths...)
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

func (c *app) scanTree(
	paths ...rootfs.Path) error {
	scanner := bistro.NewScanner(c.DI)
	var elfDeps []*bistro.ElfDependency
	for _, path := range paths {
		deps, err := scanner.Scan(path)
		if err != nil {
			return err
		}
		elfDeps = append(elfDeps, deps...)
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

func (c *app) stdout() io.Writer {
	if c.Stdout != nil {
		return c.Stdout
	}
	return os.Stdout
}

func (c *app) stderr() io.Writer {
	if c.Stderr != nil {
		return c.Stderr
	}
	return os.Stderr
}

func (c *app) printf(format string, args ...any) {
	fmt.Fprintf(c.stdout(), format, args...)
}
func (c *app) errorf(format string, args ...any) {
	fmt.Fprintf(c.stderr(), format, args...)
}

func (c *app) bundle(input bistro.BundleInput) error {
	bundler := bistro.NewBundler(c.DI)
	return bundler.Bundle(input)
}

func (c *app) absPath(p string) string {
	if path.IsAbs(p) {
		return p
	}
	return path.Join(c.Cwd, p)
}

func (c *app) rootfsPaths(paths ...string) []rootfs.Path {
	var result []rootfs.Path
	for _, p := range paths {
		result = append(result, rootfs.MustPath(c.absPath(p)))
	}
	return result
}
