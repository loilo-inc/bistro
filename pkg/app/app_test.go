package app

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundleCopyFlags(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(dir)
	for _, name := range []string{"first", "second"} {
		require.NoError(t, os.WriteFile(name, []byte(name+" content"), 0644))
	}
	out := filepath.Join(dir, "bundle.tar")
	// Exercise flag parsing and propagation to BundleInput without positional
	// ELF arguments or any dependency on host ldd/dpkg commands.
	err = New("v", "c", "d").Run(context.Background(), []string{
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
