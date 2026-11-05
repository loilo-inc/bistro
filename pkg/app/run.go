package app

import (
	"context"
	"os"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

func Run(version, commit, date string) error {
	fs := os.DirFS("/")
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ldd := ldd.NewResolver(ldd.ResolverOptions{
		LdLibraryPath: os.Getenv("LD_LIBRARY_PATH"),
		Cwd:           cwd,
		FS:            fs,
	})
	query := dpkg.NewQuery(fs)
	logger := bistro.NewLogger(os.Stdout, os.Stderr)
	cli := New(NewInput{
		Version: version,
		Commit:  commit,
		Date:    date,
		Cwd:     cwd,
		DI:      bistro.NewDI(logger, ldd, query, fs),
	})
	ctx := context.Background()
	if err := cli.Run(ctx, os.Args); err != nil {
		return err
	}
	return nil
}
