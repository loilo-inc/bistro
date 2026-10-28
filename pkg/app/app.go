package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/urfave/cli/v3"
)

func CreateCLI() {
	app := CreateCli()
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
				Name:  "save",
				Usage: "Save the list of installed Debian packages",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out",
						Aliases:  []string{"o"},
						Usage:    "Output file for the list of installed Debian packages",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return c.Save(cmd.String("out"))
				},
			},
			{
				Name:  "patch",
				Usage: "Create a root filesystem tar archive from added or updated packages",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "before",
						Usage:    "Input file containing the list of installed Debian packages",
						Required: true,
					},
					outRootfs(),
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return c.Patch(cmd.String("before"), cmd.String("out"))
				},
			},
			{
				Name:      "collect",
				Usage:     "Collect files and metadata for installed packages",
				ArgsUsage: "PACKAGE [PACKAGE...]",
				Flags:     []cli.Flag{outRootfs()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return fmt.Errorf("collect requires at least one package")
					}
					return c.Collect(cmd.String("out"), cmd.Args().Slice())
				},
			},
			{
				Name:      "bundle",
				Usage:     "Collect packages owning an executable's shared libraries",
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

func CreateCli() *app {
	execer := &bistro.DefaultExecer{}
	ldd := bistro.CreateLdd(execer)
	dpkg := bistro.CreateDpkg(execer)
	return &app{
		dpkg: dpkg,
		ldd:  ldd,
	}
}

type PackageList struct {
	Packages []*bistro.Package `json:"packages"`
}

func (c *app) Save(outFile string) error {
	list, err := c.listInstalledPackages()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outFile), 0755); err != nil {
		return err
	}
	jsonData, err := json.Marshal(&PackageList{Packages: list})
	if err != nil {
		return err
	}
	return os.WriteFile(outFile, jsonData, 0644)
}

func (c *app) Patch(inFile string, outfile string) error {
	jsonData, err := os.ReadFile(inFile)
	if err != nil {
		return err
	}
	var pkgList PackageList
	if err := json.Unmarshal(jsonData, &pkgList); err != nil {
		return err
	}
	baker := bistro.CreateBaker(c.dpkg, c.ldd)
	after, err := c.listInstalledPackages()
	if err != nil {
		return err
	}
	diff, err := bistro.DiffPackages(pkgList.Packages, after)
	if err != nil {
		return err
	}
	var copyList []*bistro.Package
	copyList = append(copyList, diff.Added...)
	copyList = append(copyList, diff.Changed...)
	return baker.CreateRootFS(outfile, copyList)
}

func (c *app) listInstalledPackages() ([]*bistro.Package, error) {
	allList, err := c.dpkg.ListPackages()
	if err != nil {
		return nil, err
	}
	var installed []*bistro.Package
	for _, pkg := range allList {
		if pkg.Status == "installed" {
			installed = append(installed, pkg)
		}
	}
	return installed, nil
}

func (c *app) Collect(outfile string, packages ...[]string) error {
	list, err := c.dpkg.ListPackages(packages...)
	if err != nil {
		return err
	}
	baker := bistro.CreateBaker(c.dpkg, c.ldd)
	return baker.CreateRootFS(outfile, list)
}

func (c *app) Bundle(outfile string, file string) error {
	finder := bistro.CreateFinder(c.dpkg, c.ldd)
	backedPackages, err := finder.PackagesForExecutable(file)
	if err != nil {
		return err
	}
	baker := bistro.CreateBaker(c.dpkg, c.ldd)
	return baker.CreateRootFS(outfile, backedPackages)
}
