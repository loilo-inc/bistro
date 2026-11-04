package app

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/bistro"
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/testfs"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These CLI tests do not scan ELF files or use the host's ld.so.cache.
type cacheOnlyResolver struct {
	t     *testing.T
	loads int
}

func (r *cacheOnlyResolver) Load() error {
	r.t.Helper()
	r.loads++
	return nil
}

func (r *cacheOnlyResolver) ListObjects(rootfs.Path) ([]*ldd.Library, error) {
	r.t.Helper()
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
				application := &runner{app: New("v", "c", "d").(*app), DI: bistro.DI{FS: os.DirFS("/")}}
				application.DI.Query = query
				resolver := &cacheOnlyResolver{t: t}
				application.DI.Ldd = resolver
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
	application := &runner{app: New("v", "c", "d").(*app), DI: bistro.DI{FS: os.DirFS("/")}}
	application.cwd = dir
	application.DI.Query = dpkg.NewQuery(testfs.Mount(os.DirFS("/"), "var/lib/dpkg", os.DirFS(baseDir)))
	resolver := &cacheOnlyResolver{t: t}
	application.DI.Ldd = resolver
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
