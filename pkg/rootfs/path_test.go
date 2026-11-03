package rootfs_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

func TestPathFilesystemAndArchiveNames(t *testing.T) {
	files := fstest.MapFS{"usr/bin/tool": {Data: []byte("tool")}}
	for _, tc := range []struct{ input, fsName, absolute, tarName string }{
		{"/", ".", "/", "."},
		{".", ".", "/", "."},
		{"/usr/bin/tool", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
		{"usr/bin/tool", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
		{"/usr//bin/../bin/tool", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			p := rootfs.NewPath(tc.input)
			require.Equal(t, tc.fsName, p.Path())
			require.Equal(t, tc.absolute, p.Abs())
			require.Equal(t, tc.tarName, p.TarName())
			require.True(t, fs.ValidPath(p.Path()))
			_, err := fs.Stat(files, p.Path())
			require.NoError(t, err)
		})
	}
	parent := rootfs.NewPath("/usr/bin/tool").Dir()
	require.Equal(t, "usr/bin", parent.Path())
	sibling := parent.Join("../share/data")
	require.Equal(t, "/usr/share/data", sibling.Abs())
	require.Equal(t, "usr/share/data", sibling.TarName())
	require.Equal(t, "usr/bin", parent.Path(), "joining a path must not change its parent")
	require.Equal(t, "/", rootfs.NewPath("/").Dir().Abs())
}
