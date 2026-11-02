package main

import (
	"context"
	"fmt"
	"os"

	"github.com/loilo-inc/bistro/pkg/app"
)

// set by goreleaser
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli := app.New(version, commit, date)
	ctx := context.Background()
	if err := cli.Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
