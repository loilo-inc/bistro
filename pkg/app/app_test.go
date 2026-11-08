package app

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/internal/teststub"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type appFixture struct {
	input    NewInput
	query    *teststub.Query
	resolver *teststub.Resolver
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
}

func newAppFixture(t *testing.T) *appFixture {
	t.Helper()
	query := teststub.NewQuery(t)
	resolver := teststub.NewResolver(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	return &appFixture{
		query: query, resolver: resolver, stdout: stdout, stderr: stderr,
		input: NewInput{
			Stdout: stdout, Stderr: stderr,
			Version: "v1.2.3", Commit: "abc123", Date: "2026-01-01", Cwd: "/workspace",
			DI: bistro.DI{
				Query: query, Ldd: resolver, FS: fstest.MapFS{},
				Logger: bistro.NewLogger(io.Discard, io.Discard),
			},
		},
	}
}

func (f *appFixture) run(args ...string) error {
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
				fixture.query.LoadErr = errors.New("unexpected database load")
				fixture.resolver.LoadErr = errors.New("unexpected ld cache load")
				args := []string{flag}
				if command != "" {
					args = []string{command, flag}
				}
				output, err := captureStdout(t, func() error { return fixture.run(args...) })
				require.NoError(t, err)
				assert.Contains(t, output, "USAGE:")
				assert.Contains(t, output, strings.TrimSpace("bistro "+command))
				assert.Zero(t, fixture.query.Loads)
				assert.Zero(t, fixture.resolver.Loads)
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
			output, err := captureStdout(t, func() error { return fixture.run(tc.flag) })
			require.NoError(t, err)
			assert.Contains(t, output, tc.want)
			assert.Zero(t, fixture.query.Loads)
			assert.Zero(t, fixture.resolver.Loads)
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
			fixture.query.LoadErr = errors.New("unexpected database load")
			fixture.resolver.LoadErr = errors.New("unexpected ld cache load")
			_, err := captureStdout(t, func() error { return fixture.run(tc.args...) })
			require.EqualError(t, err, tc.want)
			assert.Zero(t, fixture.query.Loads)
			assert.Zero(t, fixture.resolver.Loads)
		})
	}
}

func TestCommandLoadFailures(t *testing.T) {
	for _, command := range []string{"scan", "bundle"} {
		for _, stage := range []string{"dpkg", "ld cache"} {
			t.Run(command+"/"+stage, func(t *testing.T) {
				fixture := newAppFixture(t)
				failure := errors.New("load failed")
				if stage == "dpkg" {
					fixture.query.LoadErr = failure
				} else {
					fixture.resolver.LoadErr = failure
				}
				args := []string{command, "app"}
				if command == "bundle" {
					args = []string{command, "--out", "bundle.tar"}
				}
				require.ErrorIs(t, fixture.run(args...), failure)
				assert.Equal(t, 1, fixture.query.Loads)
				if stage == "dpkg" {
					assert.Zero(t, fixture.resolver.Loads)
				} else {
					assert.Equal(t, 1, fixture.resolver.Loads)
				}
			})
		}
	}
}

func TestBundlePositionalFiles(t *testing.T) {
	fixture := newAppFixture(t)
	fixture.input.DI.FS = fstest.MapFS{
		"workspace/first":  {Data: []byte("first content")},
		"workspace/second": {Data: []byte("second content")},
	}
	out := filepath.Join(t.TempDir(), "bundle.tar")
	require.NoError(t, fixture.run("bundle", "-o", out, "first", "second"))
	assert.Equal(t, 1, fixture.query.Loads)
	assert.Equal(t, 1, fixture.resolver.Loads)
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
			fixture.input.DI.FS = fstest.MapFS{
				"workspace/app":          {Data: (elftest.Fixture{}).Bytes()},
				"workspace/tools/static": {Data: (elftest.Fixture{Static: true}).Bytes()},
				"workspace/tools/README": {Data: []byte("This is a plain text file.")},
				"lib/owned.so":           {Data: []byte("owned library")},
				"lib/unowned.so":         {Data: []byte("unowned library")},
			}
			packages, err := dpkg.ParseStatus("Package: owner\nArchitecture: arm64\nMulti-Arch: same\n")
			require.NoError(t, err)
			require.Len(t, packages, 1)
			fixture.query.LookUpFunc = func(name string) (*dpkg.Package, error) {
				if name == "/lib/owned.so" {
					return packages[0], nil
				}
				return nil, nil
			}
			fixture.resolver.ListObjectsFunc = func(name rootfs.Path) ([]*ldd.Library, error) {
				if name == rootfs.MustPath("/workspace/tools/static") {
					return nil, nil
				}
				require.Equal(t, rootfs.MustPath("/workspace/app"), name)
				return []*ldd.Library{
					{Name: "owned.so", Path: rootfs.MustPath("/lib/owned.so")},
					{Name: "unowned.so", Path: rootfs.MustPath("/lib/unowned.so")},
					{Name: "missing.so"},
				}, nil
			}
			err = fixture.run(append([]string{"scan"}, append(tc.flags, "app", "tools")...)...)
			require.NoError(t, err)
			assert.Equal(t, tc.stdout, fixture.stdout.String())
			assert.Equal(t, tc.stderr, fixture.stderr.String())
			assert.Equal(t, 1, fixture.query.Loads)
			assert.Equal(t, 1, fixture.resolver.Loads)
		})
	}
}

func TestScanErrorsProduceNoPartialOutput(t *testing.T) {
	failure := errors.New("dependency scan failed")
	for _, tc := range []struct {
		name               string
		args               []string
		scanErr, errorWant error
	}{
		{name: "missing input", args: []string{"static", "absent"}, errorWant: fs.ErrNotExist},
		{name: "dependency failure", args: []string{"static", "dynamic"}, scanErr: failure, errorWant: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAppFixture(t)
			fixture.input.DI.FS = fstest.MapFS{
				"workspace/static":  {Data: (elftest.Fixture{Static: true}).Bytes()},
				"workspace/dynamic": {Data: (elftest.Fixture{}).Bytes()},
			}
			fixture.query.LookUpFunc = func(string) (*dpkg.Package, error) { return nil, nil }
			fixture.resolver.ListObjectsFunc = func(name rootfs.Path) ([]*ldd.Library, error) {
				if name == rootfs.MustPath("/workspace/static") {
					return nil, nil
				}
				return nil, tc.scanErr
			}
			err := fixture.run(append([]string{"scan"}, tc.args...)...)
			require.ErrorIs(t, err, tc.errorWant)
			assert.Empty(t, fixture.stdout.String())
			assert.Empty(t, fixture.stderr.String())
		})
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
			executable := (elftest.Fixture{}).Bytes()
			fixture.input.DI.FS = fstest.MapFS{
				"workspace/tools/cache/app":     {Data: executable},
				"workspace/tools/ignored":       {Data: executable},
				"workspace/tools/cache-old/app": {Data: executable},
				"lib/kept.so":                   {Data: []byte("library")},
			}
			fixture.query.LookUpFunc = func(string) (*dpkg.Package, error) { return nil, nil }
			var scanned []rootfs.Path
			fixture.resolver.ListObjectsFunc = func(name rootfs.Path) ([]*ldd.Library, error) {
				scanned = append(scanned, name)
				require.Equal(t, rootfs.MustPath("/workspace/tools/cache-old/app"), name)
				return []*ldd.Library{{Name: "kept.so", Path: rootfs.MustPath("/lib/kept.so")}}, nil
			}
			require.NoError(t, fixture.run("scan", "--format", tc.format,
				"--exclude", "tools/cache", "--exclude", "/workspace/tools/ignored",
				"tools", "tools/cache/app", "tools/ignored"))
			assert.Equal(t, tc.want, fixture.stdout.String())
			assert.Empty(t, fixture.stderr.String())
			assert.Equal(t, rootfs.MustPaths("/workspace/tools/cache-old/app"), scanned)
		})
	}
}

func TestBundleExcludeFlags(t *testing.T) {
	fixture := newAppFixture(t)
	executable := (elftest.Fixture{}).Bytes()
	fixture.input.DI.FS = fstest.MapFS{
		"workspace/content/cache/app":     {Data: executable},
		"workspace/content/scan-only":     {Data: executable},
		"workspace/content/copy-only":     {Data: executable},
		"workspace/content/cache-old/app": {Data: executable},
	}
	fixture.query.LookUpFunc = func(string) (*dpkg.Package, error) { return nil, nil }
	var scanned []rootfs.Path
	fixture.resolver.ListObjectsFunc = func(name rootfs.Path) ([]*ldd.Library, error) {
		scanned = append(scanned, name)
		return nil, nil
	}
	out := filepath.Join(t.TempDir(), "bundle.tar")
	require.NoError(t, fixture.run("bundle", "-o", out,
		"--exclude", "content/cache", "--exclude", "/workspace/content/scan-only",
		"--exclude-scan", "/workspace/content/copy-only",
		"content", "content/cache/app", "content/scan-only", "content/copy-only"))
	assert.Equal(t, rootfs.MustPaths("/workspace/content/cache-old/app"), scanned)
	assert.Equal(t, map[string]string{
		"workspace/content/cache-old/app": string(executable),
		"workspace/content/copy-only":     string(executable),
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
			fixture.input.DI.FS = fstest.MapFS{
				"var/lib/dpkg/info/alpha.list":      {Data: []byte("/workspace/share/alpha.txt\n")},
				"var/lib/dpkg/info/beta:arm64.list": {Data: []byte("/workspace/share/beta.txt\n")},
				"workspace/share/alpha.txt":         {Data: []byte("alpha content")},
				"workspace/share/beta.txt":          {Data: []byte("beta content")},
				"workspace/bin/app":                 {Mode: 0755, Data: executable},
				"workspace/scan-one/app":            {Data: (elftest.Fixture{}).Bytes()},
				"workspace/scan-two":                {Data: (elftest.Fixture{}).Bytes()},
			}
			packages, err := dpkg.ParseStatus("Package: alpha\nArchitecture: all\nStatus: install ok installed\n\nPackage: beta\nArchitecture: arm64\nMulti-Arch: same\nStatus: install ok installed\n")
			require.NoError(t, err)
			require.Len(t, packages, 2)
			var requested []string
			fixture.query.GetFunc = func(name string) (*dpkg.Package, error) {
				requested = append(requested, name)
				for _, pkg := range packages {
					if pkg.NameWithArch() == name {
						return pkg, nil
					}
				}
				t.Fatalf("unexpected package: %s", name)
				return nil, nil
			}
			if !tc.noScan {
				fixture.query.LookUpFunc = func(string) (*dpkg.Package, error) { return nil, nil }
			}
			var scanned []rootfs.Path
			fixture.resolver.ListObjectsFunc = func(name rootfs.Path) ([]*ldd.Library, error) {
				scanned = append(scanned, name)
				return nil, nil
			}
			args := []string{"bundle", "--out", "bundle.tar",
				"--package", "alpha", "--package", "beta:arm64"}
			if tc.noScan {
				args = append(args, "--no-scan")
			}
			args = append(args, "/workspace/bin/app", "/workspace/scan-one", "/workspace/scan-two")
			require.NoError(t, fixture.run(args...))
			assert.Equal(t, []string{"alpha", "beta:arm64"}, requested)
			if tc.noScan {
				assert.Empty(t, scanned)
			} else {
				assert.Equal(t, rootfs.MustPaths("/workspace/bin/app", "/workspace/scan-one/app", "/workspace/scan-two"), scanned)
			}
			assert.Equal(t, 1, fixture.query.Loads)
			assert.Equal(t, 1, fixture.resolver.Loads)
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
