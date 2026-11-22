package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
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
	NewInput
}

var _ App = (*app)(nil)

type NewInput struct {
	Version string
	Commit  string
	Date    string
	Cwd     string
	Stdout  io.Writer
	Stderr  io.Writer
}

func New(input NewInput) App {
	return &app{NewInput: input}
}

func load(di bistro.DI) error {
	if err := di.Query.Load(); err != nil {
		return err
	}
	if err := di.Ldd.Load(); err != nil {
		return err
	}
	return nil
}

func (c *app) command() *cli.Command {
	var rootFs string
	rootFsFlag := &cli.StringFlag{
		Name:        "rootfs",
		Usage:       "Root filesystem: tar:<path> or oci:<tar-path> from docker save (default: host filesystem)",
		Destination: &rootFs,
	}
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
					rootFsFlag,
					&cli.StringSliceFlag{
						Name:  "exclude",
						Usage: "Files or directories to exclude from scanning.",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return fmt.Errorf("scan requires at least one file or directory")
					}
					fsOpener, err := openFs(rootFs)
					if err != nil {
						return err
					}
					defer fsOpener.Close()
					fss := fsOpener.FS()
					paths := c.rootfsPaths(cmd.Args().Slice())
					excludes := c.rootfsPaths(cmd.StringSlice("exclude"))
					format := cmd.String("format")
					return c.newScanApp(scanAppInput{
						paths:    paths,
						excludes: excludes,
						format:   format,
						fs:       fss,
					}).Run(ctx)
				},
			},
			{
				Name:      "bundle",
				ArgsUsage: "BUNDLE [files or directories...]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out",
						Aliases:  []string{"o"},
						Usage:    "Output root filesystem tar archive",
						Required: true,
					},
					&cli.StringSliceFlag{
						Name:  "package",
						Usage: "Packages to include in the root filesystem",
					},
					&cli.StringSliceFlag{
						Name:  "exclude",
						Usage: "Files or directories to exclude from the root filesystem.",
					},
					&cli.StringSliceFlag{
						Name:  "exclude-scan",
						Usage: "Files or directories to exclude from scanning.",
					},
					rootFsFlag,
					&cli.BoolFlag{
						Name:  "no-scan",
						Usage: "Disable scanning for ELF dependencies",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fsHandle, err := openFs(rootFs)
					if err != nil {
						return err
					}
					defer fsHandle.Close()
					fss := fsHandle.FS()
					di := c.depsFromFs(fss)
					if err := load(di); err != nil {
						return err
					}
					return c.bundle(di, bistro.BundleInput{
						Out:          rootfs.MustPath(c.absPath(cmd.String("out"))),
						Files:        c.rootfsPaths(cmd.Args().Slice()),
						Packages:     cmd.StringSlice("package"),
						Excludes:     c.rootfsPaths(cmd.StringSlice("exclude")),
						ExcludeScans: c.rootfsPaths(cmd.StringSlice("exclude-scan")),
						NoScan:       cmd.Bool("no-scan"),
					})
				},
			},
		},
	}
}

func (c *app) Run(ctx context.Context, args []string) error {
	return c.command().Run(ctx, args)
}

func (c *app) depsFromFs(fss fs.FS) bistro.DI {
	ldd := ldd.NewResolver(ldd.ResolverOptions{
		LdLibraryPath: os.Getenv("LD_LIBRARY_PATH"),
		Cwd:           c.Cwd,
		FS:            fss,
	})
	query := dpkg.NewQuery(fss)
	logger := bistro.NewLogger(c.stdout(), c.stderr())
	return bistro.NewDI(logger, ldd, query, fss)
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

func (c *app) bundle(di bistro.DI, input bistro.BundleInput) error {
	return bistro.Bundle(di, input)
}

func (c *app) absPath(p string) string {
	if path.IsAbs(p) {
		return p
	}
	return path.Join(c.Cwd, p)
}

func (c *app) rootfsPaths(paths []string) []rootfs.Path {
	var result []rootfs.Path
	for _, p := range paths {
		result = append(result, rootfs.MustPath(c.absPath(p)))
	}
	return result
}
