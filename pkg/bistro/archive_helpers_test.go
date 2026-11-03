package bistro

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type rootFSEntry struct {
	header tar.Header
	body   string
}

func rootFSTempDir(t *testing.T) string {
	t.Helper()
	// macOS may place TempDir beneath a symlink such as /var.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func writeRootFSFile(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0755))
	require.NoError(t, os.WriteFile(name, []byte(content), mode))
	require.NoError(t, os.Chmod(name, mode))
}

func readRootFSTar(t *testing.T, data []byte) map[string]rootFSEntry {
	t.Helper()
	entries := make(map[string]rootFSEntry)
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.False(t, path.IsAbs(header.Name), "absolute archive path: %q", header.Name)
		require.NotEqual(t, "..", header.Name)
		require.False(t, strings.HasPrefix(header.Name, "../"), "invalid archive path: %q", header.Name)
		require.NotContains(t, entries, header.Name, "duplicate archive entry")
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		entries[header.Name] = rootFSEntry{header: *header, body: string(body)}
	}
	return entries
}

func rootFSArchiveName(name string) string {
	return strings.TrimPrefix(filepath.ToSlash(name), "/")
}
