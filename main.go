package main

import (
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
	if err := app.Run(version, commit, date); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
