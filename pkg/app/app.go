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
	outRootfs := func() cli.Flag {
		return &cli.StringFlag{
			Name:     "out",
			Aliases:  []string{"o"},
			Usage:    "Output root filesystem tar archive",
			Required: true,
		}
	}
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
				ArgsUsage: "EXECUTABLE",
				Flags:     []cli.Flag{outRootfs()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return fmt.Errorf("bundle requires exactly one executable")
					}
					return c.Bundle(cmd.String("out"), cmd.Args().First())
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

func (c *app) Bundle(outfile string, file string) error {
	scanner := bistro.NewScanner(c.ldd, c.dpkg)
	deps, err := scanner.Scan(file)
	if err != nil {
		return err
	}
	baker := bistro.NewBaker(c.dpkg, c.ldd)
	visitedPackages := make(map[string]struct{})
	var backedPackages []*bistro.Package
	var files []string
	for _, dep := range deps {
		if len(dep.BrokenLibs) > 0 {
			return fmt.Errorf("some libraries are not linked: %s => %v", dep.ElfFile.Path, dep.BrokenLibs)
		}
		for _, exeDep := range dep.Dependencies {
			if exeDep.Package.Status != bistro.PackageStatusInstalled {
				return fmt.Errorf("depended package is not installed: %s", exeDep.Package.Package)
			}
			if _, ok := visitedPackages[exeDep.Package.Package]; !ok {
				visitedPackages[exeDep.Package.Package] = struct{}{}
				backedPackages = append(backedPackages, exeDep.Package)
			}
		}
		files = append(files, dep.ElfFile.Path)
	}
	return baker.CreateRootFS(&bistro.BakeInput{
		Out:      outfile,
		DpkgList: backedPackages,
		Files:    files,
	})
}
