package ldd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDst(t *testing.T) {
	for _, tt := range []struct {
		arch, lib, platform string
	}{
		{"amd64", "lib/x86_64-linux-gnu", "x86_64"},
		{"arm64", "lib/aarch64-linux-gnu", "aarch64"},
		// Unsupported architectures fall back to empty tokens instead of failing.
		{"", "", ""},
		{"386", "", ""},
		{"arm", "", ""},
		{"riscv64", "", ""},
		{"x86_64", "", ""},
	} {
		t.Run(tt.arch, func(t *testing.T) {
			require.Equal(t, tt.lib, getDstLib(tt.arch))
			require.Equal(t, tt.platform, getDstPlatform(tt.arch))
		})
	}
}
