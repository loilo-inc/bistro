package app

import (
	"archive/tar"
	"bytes"
	"context"
	"debug/elf"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type appFixture struct {
	input          NewInput
	files          fstest.MapFS
	root           string
	t              *testing.T
	stdout, stderr *bytes.Buffer
}

func newAppFixture(t *testing.T) *appFixture {
	t.Helper()
	t.Setenv("LD_LIBRARY_PATH", "")
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	return &appFixture{
		t: t, root: t.TempDir(), stdout: stdout, stderr: stderr,
		files: fstest.MapFS{},
		input: NewInput{
			Stdout: stdout, Stderr: stderr,
			Version: "v1.2.3", Commit: "abc123", Date: "2026-01-01", Cwd: "/workspace",
		},
	}
}

func (f *appFixture) run(args ...string) error {
	f.t.Helper()
	if _, ok := f.files["var/lib/dpkg/status"]; !ok {
		f.files["var/lib/dpkg/status"] = &fstest.MapFile{}
	}
	for name, file := range f.files {
		if file == nil {
			continue
		}
		dest := filepath.Join(f.root, filepath.FromSlash(name))
		require.NoError(f.t, os.MkdirAll(filepath.Dir(dest), 0755))
		mode := file.Mode
		if mode == 0 {
			mode = 0644
		}
		require.NoError(f.t, os.WriteFile(dest, file.Data, mode))
	}
	if len(args) > 0 && (args[0] == "scan" || args[0] == "bundle") {
		archive := writeRootfsTar(f.t, os.DirFS(f.root))
		args = append([]string{args[0], "--rootfs", "tar:" + archive}, args[1:]...)
	}
	return New(f.input).Run(context.Background(), append([]string{"bistro"}, args...))
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	stdout := os.Stdout
	var runErr error
	func() {
		os.Stdout = file
		defer func() { os.Stdout = stdout }()
		runErr = run()
	}()
	data, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	return string(data), runErr
}

func TestHelpWithoutLoadingDependencies(t *testing.T) {
	for _, command := range []string{"", "scan", "bundle"} {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(command+"/"+flag, func(t *testing.T) {
				fixture := newAppFixture(t)
				fixture.files["var/lib/dpkg/status"] = nil
				args := []string{flag}
				if command != "" {
					args = []string{command, flag}
				}
				output, err := captureStdout(t, func() error { return fixture.run(args...) })
				require.NoError(t, err)
				assert.Contains(t, output, "USAGE:")
				assert.Contains(t, output, strings.TrimSpace("bistro "+command))
			})
		}
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"--help", "Build a distroless root filesystem"},
		{"--version", "bistro version v1.2.3 (commit: abc123, date: 2026-01-01)"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			fixture := newAppFixture(t)
			fixture.files["var/lib/dpkg/status"] = nil
			output, err := captureStdout(t, func() error { return fixture.run(tc.flag) })
			require.NoError(t, err)
			assert.Contains(t, output, tc.want)
		})
	}
}

func TestCommandValidationBeforeLoading(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"scan without paths", []string{"scan"}, "scan requires at least one file or directory"},
		{"bundle without output", []string{"bundle"}, "Required flag \"out\" not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAppFixture(t)
			fixture.files["var/lib/dpkg/status"] = nil
			_, err := captureStdout(t, func() error { return fixture.run(tc.args...) })
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestCommandLoadFailures(t *testing.T) {
	for _, command := range []string{"scan", "bundle"} {
		for _, stage := range []string{"dpkg", "ld cache"} {
			t.Run(command+"/"+stage, func(t *testing.T) {
				fixture := newAppFixture(t)
				if stage == "dpkg" {
					// A status path below a regular file cannot be read.
					fixture.files["var"] = &fstest.MapFile{Data: []byte("not a directory")}
					fixture.files["var/lib/dpkg/status"] = nil
				} else {
					fixture.files["etc/ld.so.cache"] = &fstest.MapFile{Data: []byte("invalid")}
				}
				args := []string{command, "app"}
				if command == "bundle" {
					args = []string{command, "--out", filepath.Join(t.TempDir(), "bundle.tar")}
				}
				err := fixture.run(args...)
				if stage == "dpkg" {
					var pathErr *fs.PathError
					require.ErrorAs(t, err, &pathErr)
					assert.Contains(t, pathErr.Path, "var/lib")
				} else {
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				}
				assert.Empty(t, fixture.stdout.String())
				assert.Empty(t, fixture.stderr.String())
			})
		}
	}
}

func TestBundlePositionalFiles(t *testing.T) {
	fixture := newAppFixture(t)
	fixture.files = fstest.MapFS{
		"workspace/first":  {Data: []byte("first content")},
		"workspace/second": {Data: []byte("second content")},
	}
	out := filepath.Join(t.TempDir(), "bundle.tar")
	require.NoError(t, fixture.run("bundle", "-o", out, "first", "second"))
	assert.Equal(t, map[string]string{
		"workspace/first": "first content", "workspace/second": "second content",
	}, readArchiveFiles(t, out))
}

func readArchiveFiles(t *testing.T, name string) map[string]string {
	t.Helper()
	file, err := os.Open(name)
	require.NoError(t, err)
	defer file.Close()
	reader := tar.NewReader(file)
	files := make(map[string]string)
	for {
		hdr, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NotContains(t, files, hdr.Name)
		files[hdr.Name] = string(body)
	}
	return files
}

func TestScanOutput(t *testing.T) {
	for _, tc := range []struct {
		name           string
		flags          []string
		stdout, stderr string
	}{
		{name: "default", stdout: "/workspace/app\n  /lib/owned.so => owner:arm64\n  /lib/unowned.so => <none>\n/workspace/tools/static\n", stderr: "  [BROKEN] missing.so\n"},
		{name: "tree", flags: []string{"--format", "tree"}, stdout: "/workspace/app\n  /lib/owned.so => owner:arm64\n  /lib/unowned.so => <none>\n/workspace/tools/static\n", stderr: "  [BROKEN] missing.so\n"},
		{name: "list", flags: []string{"--format", "list"}, stdout: "/workspace/app: owner:arm64 /lib/unowned.so\n/workspace/tools/static: \n", stderr: "[BROKEN] missing.so\n"},
		{name: "flat", flags: []string{"-f", "flat"}, stdout: "owner:arm64 /lib/unowned.so\n", stderr: "[BROKEN] missing.so\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAppFixture(t)
			fixture.files = fstest.MapFS{
				"workspace/app":          {Data: (elftest.Fixture{Entries: elftest.Needed("owned.so", "unowned.so", "missing.so")}).Bytes()},
				"workspace/tools/static": {Data: (elftest.Fixture{Static: true}).Bytes()},
				"workspace/tools/README": {Data: []byte("This is a plain text file.")},
				"lib/owned.so":           {Data: (elftest.Fixture{Static: true}).Bytes()},
				"lib/unowned.so":         {Data: (elftest.Fixture{Static: true}).Bytes()},
			}
			fixture.files["var/lib/dpkg/status"] = &fstest.MapFile{Data: []byte("Package: owner\nArchitecture: arm64\nMulti-Arch: same\n")}
			fixture.files["var/lib/dpkg/info/owner:arm64.list"] = &fstest.MapFile{Data: []byte("/lib/owned.so\n")}
			err := fixture.run(append([]string{"scan"}, append(tc.flags, "app", "tools")...)...)
			require.NoError(t, err)
			assert.Equal(t, tc.stdout, fixture.stdout.String())
			assert.Equal(t, tc.stderr, fixture.stderr.String())
		})
	}
}

func TestScanErrorsProduceNoPartialOutput(t *testing.T) {
	for _, format := range []string{"tree", "list", "flat"} {
		for _, target := range []string{"absent", "dynamic"} {
			t.Run(format+"/"+target, func(t *testing.T) {
				fixture := newAppFixture(t)
				fixture.files = fstest.MapFS{
					"workspace/static":  {Data: (elftest.Fixture{Static: true}).Bytes()},
					"workspace/dynamic": {Data: (elftest.Fixture{Entries: elftest.Needed("lib.so"), Omit: []elf.DynTag{elf.DT_STRTAB}}).Bytes()},
				}
				err := fixture.run("scan", "--format", format, "static", target)
				require.Error(t, err)
				if target == "absent" {
					require.ErrorIs(t, err, fs.ErrNotExist)
				} else {
					require.ErrorContains(t, err, "DT_STRTAB is missing")
				}
				assert.Empty(t, fixture.stdout.String())
				assert.Empty(t, fixture.stderr.String())
			})
		}
	}
}

func TestScanExcludeFlags(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"tree", "/workspace/tools/cache-old/app\n  /lib/kept.so => <none>\n"},
		{"list", "/workspace/tools/cache-old/app: /lib/kept.so\n"},
		{"flat", "/lib/kept.so\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			fixture := newAppFixture(t)
			executable := (elftest.Fixture{Entries: elftest.Needed("kept.so")}).Bytes()
			fixture.files = fstest.MapFS{
				"workspace/tools/cache/app":     {Data: executable},
				"workspace/tools/ignored":       {Data: executable},
				"workspace/tools/cache-old/app": {Data: executable},
				"lib/kept.so":                   {Data: (elftest.Fixture{Static: true}).Bytes()},
			}
			require.NoError(t, fixture.run("scan", "--format", tc.format,
				"--exclude", "tools/cache", "--exclude", "/workspace/tools/ignored",
				"tools", "tools/cache/app", "tools/ignored"))
			assert.Equal(t, tc.want, fixture.stdout.String())
			assert.Empty(t, fixture.stderr.String())
		})
	}
}

func TestBundleExcludeFlags(t *testing.T) {
	fixture := newAppFixture(t)
	executable := (elftest.Fixture{}).Bytes()
	fixture.files = fstest.MapFS{
		"workspace/content/cache/app":     {Data: executable},
		"workspace/content/scan-only":     {Data: executable},
		"workspace/content/copy-only":     {Data: (elftest.Fixture{Entries: elftest.Needed("lib.so"), Omit: []elf.DynTag{elf.DT_STRTAB}}).Bytes()},
		"workspace/content/cache-old/app": {Data: executable},
	}
	out := filepath.Join(t.TempDir(), "bundle.tar")
	require.NoError(t, fixture.run("bundle", "-o", out,
		"--exclude", "content/cache", "--exclude", "/workspace/content/scan-only",
		"--exclude-scan", "/workspace/content/copy-only",
		"content", "content/cache/app", "content/scan-only", "content/copy-only"))
	assert.Equal(t, map[string]string{
		"workspace/content/cache-old/app": string(executable),
		"workspace/content/copy-only":     string(fixture.files["workspace/content/copy-only"].Data),
	}, readArchiveFiles(t, out))
	assert.Empty(t, fixture.stdout.String())
	assert.Empty(t, fixture.stderr.String())
}

func TestBundleInputFlags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		noScan bool
	}{
		{name: "default"},
		{name: "no scan", noScan: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAppFixture(t)
			fixture.input.Cwd = t.TempDir()
			executable := (elftest.Fixture{Static: true}).Bytes()
			fixture.files = fstest.MapFS{
				"var/lib/dpkg/info/alpha.list":      {Data: []byte("/workspace/share/alpha.txt\n")},
				"var/lib/dpkg/info/beta:arm64.list": {Data: []byte("/workspace/share/beta.txt\n")},
				"workspace/share/alpha.txt":         {Data: []byte("alpha content")},
				"workspace/share/beta.txt":          {Data: []byte("beta content")},
				"workspace/bin/app":                 {Mode: 0755, Data: executable},
				"workspace/scan-one/app":            {Data: (elftest.Fixture{}).Bytes()},
				"workspace/scan-two":                {Data: (elftest.Fixture{}).Bytes()},
			}
			fixture.files["var/lib/dpkg/status"] = &fstest.MapFile{Data: []byte("Package: alpha\nArchitecture: all\nStatus: install ok installed\n\nPackage: beta\nArchitecture: arm64\nMulti-Arch: same\nStatus: install ok installed\n")}
			args := []string{"bundle", "--out", "bundle.tar",
				"--package", "alpha", "--package", "beta:arm64"}
			if tc.noScan {
				fixture.files["workspace/scan-two"].Data = (elftest.Fixture{Entries: elftest.Needed("lib.so"), Omit: []elf.DynTag{elf.DT_STRTAB}}).Bytes()
				args = append(args, "--no-scan")
			}
			args = append(args, "/workspace/bin/app", "/workspace/scan-one", "/workspace/scan-two")
			require.NoError(t, fixture.run(args...))
			entries := readArchiveFiles(t, filepath.Join(fixture.input.Cwd, "bundle.tar"))
			assert.Len(t, entries, 9)
			assert.Equal(t, string(executable), entries["workspace/bin/app"])
			assert.Equal(t, "alpha content", entries["workspace/share/alpha.txt"])
			assert.Equal(t, "beta content", entries["workspace/share/beta.txt"])
			for _, name := range []string{"alpha", "beta"} {
				assert.Contains(t, entries, "var/lib/dpkg/status.d/"+name)
				assert.Contains(t, entries, "var/lib/dpkg/status.d/"+name+".md5sums")
			}
			assert.Contains(t, entries, "workspace/scan-one/app")
			assert.Contains(t, entries, "workspace/scan-two")
		})
	}
}
