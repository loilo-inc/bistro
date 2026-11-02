package ldd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetLoaderVars(t *testing.T) {
	for _, tt := range []struct {
		arch string
		want LoaderVars
	}{
		{"amd64", LoaderVars{Lib: "lib/x86_64-linux-gnu", Platform: "x86_64"}},
		{"arm64", LoaderVars{Lib: "lib/aarch64-linux-gnu", Platform: "aarch64"}},
	} {
		t.Run(tt.arch, func(t *testing.T) {
			got, err := getLoaderVars(tt.arch)
			require.NoError(t, err)
			require.Equal(t, &tt.want, got)
		})
	}
}

func TestGetLoaderVarsUnsupported(t *testing.T) {
	for _, arch := range []string{"", "386", "arm", "riscv64", "x86_64", "unknown"} {
		t.Run(arch, func(t *testing.T) {
			got, err := getLoaderVars(arch)
			require.Nil(t, got)
			require.EqualError(t, err, "unsupported architecture: "+arch)
		})
	}
}
