package rootfs_test

import (
	"fmt"
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
		{"", ".", "/", "."},
		{"/usr/bin/tool", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
		{"usr/bin/tool", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
		{"/usr//bin/../bin/tool", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
		{"./usr//bin/../bin/tool/", "usr/bin/tool", "/usr/bin/tool", "usr/bin/tool"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			p := rootfs.NewPath(tc.input)
			require.Equal(t, tc.fsName, p.Path())
			require.Equal(t, tc.absolute, p.Abs())
			require.Equal(t, tc.absolute, p.String())
			require.Equal(t, tc.absolute, fmt.Sprintf("%s", p))
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

func TestPathResolve(t *testing.T) {
	base := rootfs.NewPath("/work/app")
	for _, tc := range []struct{ target, want string }{
		{"libs/lib.so", "/work/app/libs/lib.so"},
		{"./libs/../lib.so", "/work/app/lib.so"},
		{"../lib.so", "/work/lib.so"},
		{"../../../lib.so", "/lib.so"},
		{"/opt/lib/lib.so", "/opt/lib/lib.so"},
		{"/opt/../lib/lib.so", "/lib/lib.so"},
		{"/", "/"},
		{".", "/work/app"},
		{"", "/work/app"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			got := base.Resolve(tc.target)
			require.Equal(t, tc.want, got.Abs())
			require.True(t, fs.ValidPath(got.Path()))
			require.Equal(t, rootfs.NewPath(tc.want), got)
		})
	}
	require.Equal(t, "/work/app", base.Abs(), "resolving must not change the base")
}

func TestPathSplitList(t *testing.T) {
	base := rootfs.NewPath("/work/app")
	for _, tc := range []struct {
		name, list string
		want       []rootfs.Path
	}{
		{"empty list", "", nil},
		{"absolute", "/opt/lib:/usr/lib", rootfs.Paths("/opt/lib", "/usr/lib")},
		{"relative", "libs:../shared", rootfs.Paths("/work/app/libs", "/work/shared")},
		{"mixed", "libs:/opt/lib:./other/../libs", rootfs.Paths("/work/app/libs", "/opt/lib", "/work/app/libs")},
		{"empty elements", ":libs::", rootfs.Paths("/work/app", "/work/app/libs", "/work/app", "/work/app")},
		{"only empty elements", ":", rootfs.Paths("/work/app", "/work/app")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, base.SplitList(tc.list))
		})
	}
}

func TestPaths(t *testing.T) {
	require.Nil(t, rootfs.Paths())
	require.Equal(t, []rootfs.Path{
		rootfs.NewPath("/"),
		rootfs.NewPath("/usr/bin/tool"),
		rootfs.NewPath("/usr/bin/tool"),
	}, rootfs.Paths("/", "/usr//bin/tool", "./usr/bin/tool"))
}

func TestPathJoinAndDir(t *testing.T) {
	base := rootfs.NewPath("/usr/bin")
	require.Equal(t, rootfs.NewPath("/usr/bin/tool"), base.Join("tool"))
	require.Equal(t, rootfs.NewPath("/usr/share"), base.Join("../share"))
	require.Equal(t, base, base.Join(""))
	require.Equal(t, rootfs.NewPath("/usr"), base.Dir())
	require.Equal(t, rootfs.NewPath("/"), rootfs.NewPath("/").Dir())
	require.Equal(t, rootfs.NewPath("/usr/bin"), base)
}

func TestPathZeroValue(t *testing.T) {
	var p rootfs.Path
	require.Empty(t, p.Path(), "the zero value represents an unset path")
	require.Equal(t, "/", p.Abs())
	require.Equal(t, ".", p.TarName())
}
