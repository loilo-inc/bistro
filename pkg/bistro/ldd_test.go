package bistro

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLddResult(t *testing.T) {
	for _, tt := range []struct {
		name    string
		fixture string
		want    *LinkInfo
		wantErr string
	}{
		{
			name:    "empty output",
			fixture: "empty.txt",
			want:    &LinkInfo{},
		},
		{
			name:    "blank lines",
			fixture: "blank_lines.txt",
			want:    &LinkInfo{},
		},
		{
			name:    "x86_64 libraries and loader",
			fixture: "x86_64.txt",
			want: &LinkInfo{
				Vdso: "linux-vdso.so.1 (0x00007ffd12340000)",
				Ld:   "/lib64/ld-linux-x86-64.so.2 (0x00007f5678000000)",
				Libs: []*Library{
					{Name: "libfoo.so.1", Path: "/lib/x86_64-linux-gnu/libfoo.so.1"},
					{Name: "libc.so.6", Path: "/lib/x86_64-linux-gnu/libc.so.6"},
				},
			},
		},
		{
			name:    "aarch64 libraries and loader with whitespace",
			fixture: "aarch64_whitespace.txt",
			want: &LinkInfo{
				Vdso: "linux-vdso.so.1 (0x0000ffffabcd0000)",
				Ld:   "/lib/ld-linux-aarch64.so.1 (0x0000ffffac000000)",
				Libs: []*Library{{Name: "libc.so.6", Path: "/lib/aarch64-linux-gnu/libc.so.6"}},
			},
		},
		{
			name:    "missing libraries alongside resolved library",
			fixture: "missing_libraries.txt",
			want: &LinkInfo{
				Libs: []*Library{
					{Name: "libc.so.6", Path: "/lib/x86_64-linux-gnu/libc.so.6"},
					{Name: "libmissing.so.1", Path: ""},
					{Name: "libother.so.2", Path: ""},
				},
			},
		},
		{
			name:    "unexpected line",
			fixture: "unexpected_line.txt",
			wantErr: "unexpected line in ldd output: unexpected output",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join("testdata", "ldd", tt.fixture))
			require.NoError(t, err)
			got, err := parseLddResult(input)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
