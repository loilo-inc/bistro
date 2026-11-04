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
			p := rootfs.MustPath(tc.input)
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
	parent := rootfs.MustPath("/usr/bin/tool").Dir()
	require.Equal(t, "usr/bin", parent.Path())
	sibling := parent.Join("../share/data")
	require.Equal(t, "/usr/share/data", sibling.Abs())
	require.Equal(t, "usr/share/data", sibling.TarName())
	require.Equal(t, "usr/bin", parent.Path(), "joining a path must not change its parent")
	require.Equal(t, "/", rootfs.MustPath("/").Dir().Abs())
}

func TestPathResolve(t *testing.T) {
	base := rootfs.MustPath("/work/app")
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
			require.Equal(t, rootfs.MustPath(tc.want), got)
		})
	}
	require.Equal(t, "/work/app", base.Abs(), "resolving must not change the base")
}

func TestPathSplitList(t *testing.T) {
	base := rootfs.MustPath("/work/app")
	for _, tc := range []struct {
		name, list string
		want       []rootfs.Path
	}{
		{"empty list", "", nil},
		{"absolute", "/opt/lib:/usr/lib", rootfs.MustPaths("/opt/lib", "/usr/lib")},
		{"relative", "libs:../shared", rootfs.MustPaths("/work/app/libs", "/work/shared")},
		{"mixed", "libs:/opt/lib:./other/../libs", rootfs.MustPaths("/work/app/libs", "/opt/lib", "/work/app/libs")},
		{"empty elements", ":libs::", rootfs.MustPaths("/work/app", "/work/app/libs", "/work/app", "/work/app")},
		{"only empty elements", ":", rootfs.MustPaths("/work/app", "/work/app")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, base.SplitList(tc.list))
		})
	}
}

func TestPaths(t *testing.T) {
	require.Nil(t, rootfs.MustPaths())
	require.Equal(t, []rootfs.Path{
		rootfs.MustPath("/"),
		rootfs.MustPath("/usr/bin/tool"),
		rootfs.MustPath("/usr/bin/tool"),
	}, rootfs.MustPaths("/", "/usr//bin/tool", "./usr/bin/tool"))
}

func TestNewPathValidation(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		invalid     bool
	}{
		{input: "..", invalid: true},
		{input: "../etc", invalid: true},
		{input: "./../etc", invalid: true},
		{input: "a/../../etc", invalid: true},
		{input: "a/../..", invalid: true},
		{input: "..cache/file", want: "..cache/file"},
		{input: ".../file", want: ".../file"},
		{input: "a/../etc", want: "etc"},
		{input: "/usr/../etc", want: "etc"},
		{input: "", want: "."},
	} {
		t.Run(tc.input, func(t *testing.T) {
			p, err := rootfs.NewPath(tc.input)
			if tc.invalid {
				require.ErrorContains(t, err, "path cannot start with '..'")
				require.Equal(t, rootfs.Path{}, p)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, p.Path())
			require.Equal(t, rootfs.MustPath(tc.input), p)
		})
	}
}

func TestPathsValidation(t *testing.T) {
	paths, err := rootfs.Paths()
	require.NoError(t, err)
	require.Nil(t, paths)
	paths, err = rootfs.Paths("/usr/bin", "./etc", ".")
	require.NoError(t, err)
	require.Equal(t, rootfs.MustPaths("/usr/bin", "etc", "/"), paths)
	for _, inputs := range [][]string{
		{"../etc", "/usr"},
		{"/usr", "a/../../etc", "/var"},
		{"/usr", ".."},
	} {
		paths, err := rootfs.Paths(inputs...)
		require.ErrorContains(t, err, "path cannot start with '..'")
		require.Nil(t, paths, "invalid input must not return a partial result")
	}
}

func TestMustPathPanicsOnInvalidInput(t *testing.T) {
	require.Panics(t, func() { rootfs.MustPath("../etc") })
	require.Panics(t, func() { rootfs.MustPaths("/usr", "a/../../etc") })
	require.Panics(t, func() { rootfs.MustPath("/").Join("../etc") })
}

func TestPathJoinAndDir(t *testing.T) {
	base := rootfs.MustPath("/usr/bin")
	require.Equal(t, rootfs.MustPath("/usr/bin/tool"), base.Join("tool"))
	require.Equal(t, rootfs.MustPath("/usr/share"), base.Join("../share"))
	require.Equal(t, base, base.Join(""))
	require.Equal(t, rootfs.MustPath("/usr"), base.Dir())
	require.Equal(t, rootfs.MustPath("/"), rootfs.MustPath("/").Dir())
	require.Equal(t, rootfs.MustPath("/usr/bin"), base)
}

func TestPathZeroValue(t *testing.T) {
	var p rootfs.Path
	require.Empty(t, p.Path(), "the zero value represents an unset path")
	require.Equal(t, "/", p.Abs())
	require.Equal(t, ".", p.TarName())
}
