package app

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/urfave/cli/v3"
)

type App interface {
	Run(ctx context.Context, args []string) error
}

type app struct {
	version string
	commit  string
	date    string
}

var _ App = (*app)(nil)

func New(version, commit, date string) App {
	return &app{
		version: version,
		commit:  commit,
		date:    date,
	}
}

func (c *runner) load() error {
	if err := c.DI.Query.Load(); err != nil {
		return err
	}
	if err := c.DI.Ldd.Load(); err != nil {
		return err
	}
	return nil
}

func (c *runner) command() *cli.Command {
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
		},
	}
}

type runner struct {
	bistro.DI
	*app
	cwd string
}

func (c *app) Run(ctx context.Context, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	fs := os.DirFS("/")
	ldd := ldd.NewResolver(ldd.ResolverOptions{
		Cwd:           cwd,
		LdLibraryPath: os.Getenv("LD_LIBRARY_PATH"),
		FS:            fs,
	})
	query := dpkg.NewQuery(fs)
	logger := bistro.NewLogger(os.Stdout, os.Stderr)
	r := &runner{
		DI: bistro.DI{
			Query:  query,
			Ldd:    ldd,
			Logger: logger,
			FS:     fs,
		},
		app: c,
		cwd: cwd,
	}
	return r.command().Run(ctx, args)
}

func (c *runner) scan(paths ...rootfs.Path) error {
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

func (c *runner) bundle(input bistro.BundleInput) error {
	bundler := bistro.NewBundler(c.DI)
	return bundler.Bundle(input)
}

func (c *runner) absPath(p string) string {
	if path.IsAbs(p) {
		return p
	}
	return path.Join(c.cwd, p)
}

func (c *runner) rootfsPaths(paths ...string) []rootfs.Path {
	var result []rootfs.Path
	for _, p := range paths {
		result = append(result, rootfs.MustPath(c.absPath(p)))
	}
	return result
}
