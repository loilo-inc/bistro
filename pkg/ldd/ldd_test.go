package ldd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLddResult(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		want    *LinkInfo
		wantErr string
	}{
		{
			name: "empty output",
			want: &LinkInfo{},
		},
		{
			name:  "blank lines",
			input: "\n \t\n\n",
			want:  &LinkInfo{},
		},
		{
			name: "x86_64 libraries and loader",
			input: "\tlinux-vdso.so.1 (0x00007ffd12340000)\n" +
				"\tlibfoo.so.1 => /lib/x86_64-linux-gnu/libfoo.so.1 (0x00007f1234000000)\n" +
				"\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x00007f2345000000)\n" +
				"\t/lib64/ld-linux-x86-64.so.2 (0x00007f5678000000)\n",
			want: &LinkInfo{
				Vdso: "linux-vdso.so.1 (0x00007ffd12340000)",
				Ld:   "/lib64/ld-linux-x86-64.so.2 (0x00007f5678000000)",
				Libs: []Library{
					{Library: "libfoo.so.1", Path: "/lib/x86_64-linux-gnu/libfoo.so.1"},
					{Library: "libc.so.6", Path: "/lib/x86_64-linux-gnu/libc.so.6"},
				},
			},
		},
		{
			name: "aarch64 libraries and loader with whitespace",
			input: "\n  linux-vdso.so.1 (0x0000ffffabcd0000)  \n" +
				"  libc.so.6  =>   /lib/aarch64-linux-gnu/libc.so.6 (0x0000ffffab000000)  \n" +
				"  /lib/ld-linux-aarch64.so.1 (0x0000ffffac000000)  \n\n",
			want: &LinkInfo{
				Vdso: "linux-vdso.so.1 (0x0000ffffabcd0000)",
				Ld:   "/lib/ld-linux-aarch64.so.1 (0x0000ffffac000000)",
				Libs: []Library{{Library: "libc.so.6", Path: "/lib/aarch64-linux-gnu/libc.so.6"}},
			},
		},
		{
			name: "missing libraries alongside resolved library",
			input: "\tlibmissing.so.1 => not found\n" +
				"\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x00007f2345000000)\n" +
				"\tlibother.so.2 => not found\n",
			want: &LinkInfo{
				Libs:       []Library{{Library: "libc.so.6", Path: "/lib/x86_64-linux-gnu/libc.so.6"}},
				BrokenLibs: []string{"libmissing.so.1", "libother.so.2"},
			},
		},
		{
			name:    "unexpected line",
			input:   "\tlinux-vdso.so.1 (0x00007ffd12340000)\n\tunexpected output\n",
			wantErr: "unexpected line in ldd output: unexpected output",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLddResult([]byte(tt.input))
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
