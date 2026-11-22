package app

import (
	"context"
	"os"
)

func Run(version, commit, date string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cli := New(NewInput{
		Version: version,
		Commit:  commit,
		Date:    date,
		Cwd:     cwd,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
	ctx := context.Background()
	if err := cli.Run(ctx, os.Args); err != nil {
		return err
	}
	return nil
}
