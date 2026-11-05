package app

import (
	"context"
	"fmt"
	"path"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/rootfs"
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
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return fmt.Errorf("scan requires at least one file or directory")
					}
					if err := c.load(); err != nil {
						return err
					}
					return c.scan(c.rootfsPaths(cmd.Args().Slice()...)...)
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
			{Name: "unpack",
				ArgsUsage: "UNPACK [tarFiles...]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "dest",
						Aliases: []string{"d"},
						Usage:   "Destination directory to unpack into",
						Value:   ".",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return c.unpack(cmd.String("dest"), cmd.Args().Slice())
				},
			},
		},
	}
}

func (c *app) Run(ctx context.Context, args []string) error {
	return c.command().Run(ctx, args)
}

func (c *app) scan(paths ...rootfs.Path) error {
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
		fmt.Printf("%s\n", elfDep.ElfFile.Path)
		for _, exeDep := range elfDep.Dependencies {
			pkgName := "<none>"
			if exeDep.Package != nil {
				pkgName = exeDep.Package.NameWithArch()
			}
			fmt.Printf("  %s => %s\n", exeDep.Library.Path, pkgName)
		}
		for _, broken := range elfDep.BrokenLibs {
			fmt.Printf("  [BROKEN] %s\n", broken)
		}
	}
	return nil
}

func (c *app) bundle(input bistro.BundleInput) error {
	bundler := bistro.NewBundler(c.DI)
	return bundler.Bundle(input)
}

func (c *app) unpack(dest string, files []string) error {
	unpacker := bistro.NewUnpacker(c.absPath(dest))
	for _, file := range files {
		if err := unpacker.Unpack(c.absPath(file)); err != nil {
			return err
		}
	}
	return nil
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
