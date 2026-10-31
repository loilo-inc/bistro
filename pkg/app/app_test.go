package app

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelpWithoutDpkgDatabase(t *testing.T) {
	for _, command := range []string{"", "scan", "bundle"} {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(command+"/"+flag, func(t *testing.T) {
				query := dpkg.NewQueryWithBaseDir(t.TempDir())
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
				application := New("v", "c", "d").(*app)
				application.DI.Query = query
				args := []string{"bistro"}
				if command != "" {
					args = append(args, command)
				}
				args = append(args, flag)
				require.NoError(t, application.Run(context.Background(), args))
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
	application := New("v", "c", "d").(*app)
	application.DI.Query = dpkg.NewQueryWithBaseDir(baseDir)
	err = application.Run(context.Background(), []string{
		"bistro", "bundle", "-o", out, "--copy", "first", "--copy", "second",
	})
	require.NoError(t, err)
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
