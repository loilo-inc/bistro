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
	"github.com/loilo-inc/bistro/pkg/internal/testfs"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unconfigured dependency scans fail instead of using the host's ld.so.cache.
type fixtureResolver struct {
	t           *testing.T
	loads       int
	loadErr     error
	listObjects func(rootfs.Path) ([]*ldd.Library, error)
}

func (r *fixtureResolver) Load() error {
	r.t.Helper()
	r.loads++
	return r.loadErr
}

func (r *fixtureResolver) ListObjects(path rootfs.Path) ([]*ldd.Library, error) {
	r.t.Helper()
	if r.listObjects != nil {
		return r.listObjects(path)
	}
	r.t.Fatal("unexpected ELF dependency scan")
	return nil, nil
}

func TestHelpWithoutDpkgDatabase(t *testing.T) {
	for _, command := range []string{"", "scan", "bundle"} {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(command+"/"+flag, func(t *testing.T) {
				query := dpkg.NewQuery(os.DirFS(t.TempDir()))
				require.ErrorIs(t, query.Load(), os.ErrNotExist)

				reader, writer, err := os.Pipe()
				require.NoError(t, err)
				stdout := os.Stdout
				os.Stdout = writer
				t.Cleanup(func() {
					os.Stdout = stdout
					_ = writer.Close()
					_ = reader.Close()
				})
				application := &runner{app: New("v", "c", "d").(*app), di: bistro.DI{FS: os.DirFS("/")}}
				application.di.Query = query
				resolver := &fixtureResolver{t: t}
				application.di.Ldd = resolver
				args := []string{"bistro"}
				if command != "" {
					args = append(args, command)
				}
				args = append(args, flag)
				require.NoError(t, application.command().Run(context.Background(), args))
				assert.Zero(t, resolver.loads)
				require.NoError(t, writer.Close())
				output, err := io.ReadAll(reader)
				require.NoError(t, err)
				assert.Contains(t, string(output), "USAGE:")
				assert.Contains(t, string(output), strings.TrimSpace("bistro "+command))
			})
		}
	}
}

func TestBundleCopyFlags(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(dir)
	for _, name := range []string{"first", "second"} {
		require.NoError(t, os.WriteFile(name, []byte(name+" content"), 0644))
	}
	out := filepath.Join(dir, "bundle.tar")
	// Use an empty database so CLI loading does not depend on the host's dpkg.
	baseDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "status"), nil, 0644))
	application := &runner{app: New("v", "c", "d").(*app), di: bistro.DI{FS: os.DirFS("/")}}
	application.cwd = dir
	application.di.Query = dpkg.NewQuery(testfs.Mount(os.DirFS("/"), "var/lib/dpkg", os.DirFS(baseDir)))
	resolver := &fixtureResolver{t: t}
	application.di.Ldd = resolver
	err = application.command().Run(context.Background(), []string{
		"bistro", "bundle", "-o", out, "--copy", "first", "--copy", "second",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, resolver.loads)
	f, err := os.Open(out)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	reader := tar.NewReader(f)
	files := make(map[string]string)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if header.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NotContains(t, files, header.Name)
		files[header.Name] = string(body)
	}
	want := make(map[string]string)
	for _, name := range []string{"first", "second"} {
		want[strings.TrimPrefix(filepath.ToSlash(filepath.Join(dir, name)), "/")] = name + " content"
	}
	assert.Equal(t, want, files)
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

func newFixtureRunner(t *testing.T, fss fs.FS, resolver ldd.Resolver) *runner {
	t.Helper()
	return &runner{
		app: New("v1.2.3", "abc123", "2026-01-01").(*app),
		cwd: "/workspace",
		di: bistro.DI{
			FS: fss, Query: dpkg.NewQuery(fss), Ldd: resolver,
			Logger: bistro.NewLogger(io.Discard, io.Discard),
		},
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"--help", "Build a distroless root filesystem"},
		{"--version", "bistro version v1.2.3 (commit: abc123, date: 2026-01-01)"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			output, err := captureStdout(t, func() error {
				return New("v1.2.3", "abc123", "2026-01-01").Run(context.Background(), []string{"bistro", tc.flag})
			})
			require.NoError(t, err)
			assert.Contains(t, output, tc.want)
		})
	}
}

type failingLoadQuery struct {
	dpkg.Query
	err   error
	loads int
}

func (q *failingLoadQuery) Load() error { q.loads++; return q.err }

func TestCommandValidationBeforeLoading(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"scan without paths", []string{"bistro", "scan"}, "scan requires at least one file or directory"},
		{"bundle without output", []string{"bistro", "bundle"}, "Required flag \"out\" not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := &failingLoadQuery{err: errors.New("unexpected database load")}
			resolver := &fixtureResolver{t: t}
			application := newFixtureRunner(t, fstest.MapFS{}, resolver)
			application.di.Query = query
			cmd := application.command()
			cmd.Writer = io.Discard
			cmd.ErrWriter = io.Discard
			require.EqualError(t, cmd.Run(context.Background(), tc.args), tc.want)
			assert.Zero(t, query.loads)
			assert.Zero(t, resolver.loads)
		})
	}
}

func TestCommandLoadFailures(t *testing.T) {
	for _, command := range []string{"scan", "bundle"} {
		for _, stage := range []string{"dpkg", "ld cache"} {
			t.Run(command+"/"+stage, func(t *testing.T) {
				failure := errors.New("load failed")
				query := &failingLoadQuery{}
				resolver := &fixtureResolver{t: t}
				if stage == "dpkg" {
					query.err = failure
				} else {
					resolver.loadErr = failure
				}
				application := newFixtureRunner(t, fstest.MapFS{}, resolver)
				application.di.Query = query
				args := []string{"bistro", command, "app"}
				if command == "bundle" {
					args = []string{"bistro", "bundle", "--out", filepath.Join(t.TempDir(), "bundle.tar")}
				}
				require.ErrorIs(t, application.command().Run(context.Background(), args), failure)
				assert.Equal(t, 1, query.loads)
				if stage == "dpkg" {
					assert.Zero(t, resolver.loads)
				} else {
					assert.Equal(t, 1, resolver.loads)
				}
			})
		}
	}
}

func TestScanOutput(t *testing.T) {
	fss := fstest.MapFS{
		"var/lib/dpkg/status":                {Data: []byte("Package: owner\nArchitecture: arm64\nMulti-Arch: same\nStatus: install ok installed\n")},
		"var/lib/dpkg/info/owner:arm64.list": {Data: []byte("/lib/owned.so\n")},
		"workspace/app":                      {Mode: 0755, Data: (elftest.Fixture{}).Bytes()},
		"workspace/tools/static":             {Mode: 0755, Data: (elftest.Fixture{Static: true}).Bytes()},
		"workspace/tools/README":             {Data: []byte("This is a plain text file.")},
		"lib/owned.so":                       {Data: []byte("owned library")},
		"lib/unowned.so":                     {Data: []byte("unowned library")},
	}
	resolver := &fixtureResolver{t: t, listObjects: func(path rootfs.Path) ([]*ldd.Library, error) {
		if path == rootfs.MustPath("/workspace/tools/static") {
			return nil, nil
		}
		require.Equal(t, rootfs.MustPath("/workspace/app"), path)
		return []*ldd.Library{
			{Name: "owned.so", Path: rootfs.MustPath("/lib/owned.so")},
			{Name: "unowned.so", Path: rootfs.MustPath("/lib/unowned.so")},
			{Name: "missing.so"},
		}, nil
	}}
	application := newFixtureRunner(t, fss, resolver)
	output, err := captureStdout(t, func() error {
		return application.command().Run(context.Background(), []string{"bistro", "scan", "app", "tools"})
	})
	require.NoError(t, err)
	assert.Equal(t, "/workspace/app\n  /lib/owned.so => owner:arm64\n  /lib/unowned.so => <none>\n  [BROKEN] missing.so\n/workspace/tools/static\n", output)
	assert.Equal(t, 1, resolver.loads)
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
			fss := fstest.MapFS{
				"var/lib/dpkg/status": {},
				"workspace/static":    {Data: (elftest.Fixture{Static: true}).Bytes()},
				"workspace/dynamic":   {Data: (elftest.Fixture{}).Bytes()},
			}
			resolver := &fixtureResolver{t: t, listObjects: func(path rootfs.Path) ([]*ldd.Library, error) {
				if path == rootfs.MustPath("/workspace/static") {
					return nil, nil
				}
				return nil, tc.scanErr
			}}
			application := newFixtureRunner(t, fss, resolver)
			output, err := captureStdout(t, func() error {
				return application.command().Run(context.Background(), append([]string{"bistro", "scan"}, tc.args...))
			})
			require.ErrorIs(t, err, tc.errorWant)
			assert.Empty(t, output)
		})
	}
}

func TestBundleInputFlags(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	executable := (elftest.Fixture{Static: true}).Bytes()
	fss := fstest.MapFS{
		"var/lib/dpkg/status":               {Data: []byte("Package: alpha\nArchitecture: all\nStatus: install ok installed\n\nPackage: beta\nArchitecture: arm64\nMulti-Arch: same\nStatus: install ok installed\n")},
		"var/lib/dpkg/info/alpha.list":      {Data: []byte("/workspace/share/alpha.txt\n")},
		"var/lib/dpkg/info/beta:arm64.list": {Data: []byte("/workspace/share/beta.txt\n")},
		"workspace/share/alpha.txt":         {Data: []byte("alpha content")},
		"workspace/share/beta.txt":          {Data: []byte("beta content")},
		"workspace/bin/app":                 {Mode: 0755, Data: executable},
		"workspace/scan-one/app":            {Data: (elftest.Fixture{}).Bytes()},
		"workspace/scan-two":                {Data: (elftest.Fixture{}).Bytes()},
	}
	var scanned []rootfs.Path
	resolver := &fixtureResolver{t: t, listObjects: func(path rootfs.Path) ([]*ldd.Library, error) { scanned = append(scanned, path); return nil, nil }}
	application := newFixtureRunner(t, fss, resolver)
	// Resolve the output relative to cwd; inputs remain in the virtual workspace.
	application.cwd = dir
	err = application.command().Run(context.Background(), []string{
		"bistro", "bundle", "--out", "bundle.tar",
		"--scan", "/workspace/scan-one", "--scan", "/workspace/scan-two",
		"--package", "alpha", "--package", "beta:arm64", "/workspace/bin/app",
	})
	require.NoError(t, err)
	assert.Equal(t, rootfs.MustPaths("/workspace/bin/app", "/workspace/scan-one/app", "/workspace/scan-two"), scanned)
	assert.Equal(t, 1, resolver.loads)
	data, err := os.ReadFile(filepath.Join(dir, "bundle.tar"))
	require.NoError(t, err)
	entries := map[string][]byte{}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if header.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NotContains(t, entries, header.Name)
		entries[header.Name] = body
	}
	assert.Len(t, entries, 7)
	assert.Equal(t, executable, entries["workspace/bin/app"])
	assert.Equal(t, []byte("alpha content"), entries["workspace/share/alpha.txt"])
	assert.Equal(t, []byte("beta content"), entries["workspace/share/beta.txt"])
	for _, name := range []string{"alpha", "beta"} {
		assert.Contains(t, entries, "var/lib/dpkg/status.d/"+name)
		assert.Contains(t, entries, "var/lib/dpkg/status.d/"+name+".md5sums")
	}
	assert.NotContains(t, entries, "workspace/scan-one/app")
	assert.NotContains(t, entries, "workspace/scan-two")
}
