package ldd

import (
	"fmt"
)

type LoaderVars struct {
	Lib      string
	Platform string
}

func getLoaderVars(arch string) (*LoaderVars, error) {
	switch arch {
	case "amd64":
		lib := "lib/x86_64-linux-gnu"
		// TODO: "haswell" or "xeon_phi" can't be detected in runtime
		return &LoaderVars{Lib: lib, Platform: "x86_64"}, nil
	case "arm64":
		lib := "lib/aarch64-linux-gnu"
		return &LoaderVars{Lib: lib, Platform: "aarch64"}, nil
	}
	return nil, fmt.Errorf("unsupported architecture: %s", arch)
}
