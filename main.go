package main

import (
	"context"
	"fmt"
	"os"

	"github.com/loilo-inc/bistro/pkg/app"
	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

// set by goreleaser
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli := newApp()
	ctx := context.Background()
	if err := cli.Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newApp() app.App {
	fs := os.DirFS("/")
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ldd := ldd.NewResolver(ldd.ResolverOptions{
		Cwd:           cwd,
		LdLibraryPath: os.Getenv("LD_LIBRARY_PATH"),
		FS:            fs,
	})
	query := dpkg.NewQuery(fs)
	logger := bistro.NewLogger(os.Stdout, os.Stderr)
	return app.New(app.NewInput{
		Version: version,
		Commit:  commit,
		Date:    date,
		Cwd:     cwd,
		DI:      bistro.NewDI(logger, ldd, query, fs),
	})
}
