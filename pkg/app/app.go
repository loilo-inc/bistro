package app

import (
	"context"
	"fmt"
	"os"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/urfave/cli/v3"
)

type App interface {
	Run(ctx context.Context, args []string) error
}

type app struct {
	DI      bistro.DI
	version string
	commit  string
	date    string
}

var _ App = (*app)(nil)

func New(version, commit, date string) App {
	execer := bistro.NewExecer()
	ldd := bistro.NewLdd(execer)
	dpkg := bistro.NewDpkg(execer)
	logger := bistro.NewLogger(os.Stdout, os.Stderr)

	return &app{
		DI:      bistro.DI{Dpkg: dpkg, Ldd: ldd, Logger: logger},
		version: version,
		commit:  commit,
		date:    date,
	}
}

func (c *app) command() *cli.Command {
	return &cli.Command{
		Name:    "bistro",
		Usage:   "Build a distroless root filesystem",
		Version: fmt.Sprintf("%s (commit: %s, date: %s)", c.version, c.commit, c.date),
		Commands: []*cli.Command{
			{
				Name:      "scan",
				ArgsUsage: "SCAN [fileorDirs...]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return fmt.Errorf("scan requires at least one file or directory")
					}
					return c.scan(cmd.Args().Slice()...)
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
					return c.bundle(bistro.BundleInput{
						Out:      cmd.String("out"),
						Files:    cmd.Args().Slice(),
						Scans:    cmd.StringSlice("scan"),
						Packages: cmd.StringSlice("package"),
						Copies:   cmd.StringSlice("copy"),
					})
				},
			},
		},
	}
}

func (c *app) Run(ctx context.Context, args []string) error {
	return c.command().Run(ctx, args)
}

func (c *app) scan(paths ...string) error {
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
				pkgName = exeDep.Package.Package
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
