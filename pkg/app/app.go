package app

import (
	"context"
	"fmt"
	"os"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/execer"
	"github.com/loilo-inc/bistro/pkg/ldd"
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
	outputFlag := func() cli.Flag {
		return &cli.StringFlag{
			Name:     "output",
			Aliases:  []string{"o"},
			Usage:    "Destination root filesystem directory",
			Required: true,
		}
	}
	return &cli.Command{
		Name:  "bistro",
		Usage: "Build a distroless root filesystem",
		Commands: []*cli.Command{
			{
				Name:  "dpkg",
				Usage: "Collect installed Debian packages",
				Commands: []*cli.Command{
					{
						Name:      "record",
						Usage:     "Run a command and collect newly installed packages",
						ArgsUsage: "-- COMMAND [ARGS...]",
						Flags:     []cli.Flag{outputFlag()},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							args := cmd.Args().Slice()
							if len(args) == 0 {
								return fmt.Errorf("dpkg record requires a command")
							}
							return c.DpkgRecord(cmd.String("output"), args[0], args[1:]...)
						},
					},
					{
						Name:      "collect",
						Usage:     "Collect files and metadata for installed packages",
						ArgsUsage: "PACKAGE [PACKAGE...]",
						Flags:     []cli.Flag{outputFlag()},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() == 0 {
								return fmt.Errorf("dpkg collect requires at least one package")
							}
							return c.DpkgCollect(cmd.String("output"), cmd.Args().Slice())
						},
					},
				},
			},
			{
				Name:  "ldd",
				Usage: "Collect shared library packages for an executable",
				Commands: []*cli.Command{
					{
						Name:      "collect",
						Usage:     "Collect packages owning an executable's shared libraries",
						ArgsUsage: "EXECUTABLE",
						Flags:     []cli.Flag{outputFlag()},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() != 1 {
								return fmt.Errorf("ldd collect requires exactly one executable")
							}
							return c.LddCollect(cmd.String("output"), cmd.Args().First())
						},
					},
				},
			},
		},
	}
}

type app struct {
	dpkg dpkg.Dpkg
	ldd  ldd.Ldd
}

func CreateCli() *app {
	execer := &execer.DefaultExecer{}
	ldd := ldd.CreateLdd(execer)
	dpkg := dpkg.CreateDpkg(execer)
	return &app{
		dpkg: dpkg,
		ldd:  ldd,
	}
}

func (c *app) DpkgRecord(root string, cmdName string, cmdArgs ...string) error {
	recorder := bistro.CreateRecorder(c.dpkg, c.ldd)
	baker := bistro.CreateBaker(c.dpkg, c.ldd)
	packages, err := recorder.RecordSubcommand(os.Stdout, cmdName, cmdArgs...)
	if err != nil {
		return err
	}
	return baker.CreateRootFS(root, packages)
}

func (c *app) DpkgCollect(dstRoot string, packages ...[]string) error {
	list, err := c.dpkg.ListPackages(packages...)
	if err != nil {
		return err
	}
	baker := bistro.CreateBaker(c.dpkg, c.ldd)
	return baker.CreateRootFS(dstRoot, list)
}

func (c *app) LddCollect(dstRoot string, file string) error {
	finder := bistro.CreateFinder(c.dpkg, c.ldd)
	backedPackages, err := finder.PackagesForExecutable(file)
	if err != nil {
		return err
	}
	baker := bistro.CreateBaker(c.dpkg, c.ldd)
	return baker.CreateRootFS(dstRoot, backedPackages)
}
