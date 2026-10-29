package app

import (
	"context"
	"fmt"
	"os"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/urfave/cli/v3"
)

func CreateCLI() {
	app := NewCli()
	err := app.command().Run(context.Background(), os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (c *app) command() *cli.Command {
	return &cli.Command{
		Name:  "bistro",
		Usage: "Build a distroless root filesystem",
		Commands: []*cli.Command{
			{
				Name:      "scan",
				ArgsUsage: "SCAN [fileorDirs...]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return fmt.Errorf("scan requires at least one file or directory")
					}
					return c.Scan(cmd.Args().Slice()...)
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
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return c.Bundle(bistro.BundleInput{
						Out:      cmd.String("out"),
						Files:    cmd.Args().Slice(),
						Scans:    cmd.StringSlice("scan"),
						Packages: cmd.StringSlice("package"),
					})
				},
			},
		},
	}
}

type app struct {
	dpkg bistro.Dpkg
	ldd  bistro.Ldd
}

func NewCli() *app {
	execer := bistro.NewExecer()
	ldd := bistro.NewLdd(execer)
	dpkg := bistro.NewDpkg(execer)
	return &app{
		dpkg: dpkg,
		ldd:  ldd,
	}
}

func (c *app) Scan(paths ...string) error {
	scanner := bistro.NewScanner(c.ldd, c.dpkg)
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
			fmt.Printf("  %s => %s\n", exeDep.Library.Path, exeDep.Package.Package)
		}
		for _, broken := range elfDep.BrokenLibs {
			fmt.Printf("  [BROKEN] %s\n", broken)
		}
	}
	return nil
}

func (c *app) Bundle(input bistro.BundleInput) error {
	bundler := bistro.NewBundler(c.dpkg, c.ldd)
	return bundler.Bundle(input)
}
