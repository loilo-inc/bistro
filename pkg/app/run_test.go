package app

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRunArgs(t *testing.T, args ...string) {
	t.Helper()
	previous := os.Args
	os.Args = append([]string{"bistro"}, args...)
	t.Cleanup(func() { os.Args = previous })
}

func TestRunEntrypointHelpAndVersion(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"--help", "Build a distroless root filesystem"},
		{"--version", "bistro version v1.2.3 (commit: abc123, date: 2026-01-01)"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			setRunArgs(t, tc.flag)
			output, err := captureStdout(t, func() error { return Run("v1.2.3", "abc123", "2026-01-01") })
			require.NoError(t, err)
			assert.Contains(t, output, tc.want)
		})
	}
}

func TestRunEntrypointReturnsCommandError(t *testing.T) {
	setRunArgs(t, "scan")
	require.EqualError(t, Run("dev", "none", "unknown"), "scan requires at least one file or directory")
}

func TestRunEntrypointReturnsWorkingDirectoryError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged user")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	t.Setenv("PWD", dir)
	require.NoError(t, os.Chmod(dir, 0000))
	setRunArgs(t, "--version")
	require.ErrorIs(t, Run("dev", "none", "unknown"), os.ErrPermission)
}

func TestRunEntrypointUnpackResolvesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LD_LIBRARY_PATH", filepath.Join(dir, "missing-libraries"))
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	content := []byte("configuration")
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "etc/app.conf", Typeflag: tar.TypeReg, Mode: 0640, Size: int64(len(content))}))
	_, err := writer.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.tar"), buf.Bytes(), 0600))
	setRunArgs(t, "unpack", "input.tar", "-d", "output")
	require.NoError(t, Run("dev", "none", "unknown"))
	name := filepath.Join(dir, "output", "etc", "app.conf")
	actual, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	info, err := os.Stat(name)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0640), info.Mode().Perm())
}
