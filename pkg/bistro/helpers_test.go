package bistro

import (
	"archive/tar"
	"bytes"
	"debug/elf"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/internal/teststub"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

type stubFinder struct {
	trace       func(string) (*ExeTraceResult, error)
	findPackage func(string) (*dpkg.Package, error)
}

func (s stubFinder) TraceDepsForExecutable(path rootfs.Path) (*ExeTraceResult, error) {
	return s.trace(path.Abs())
}
func (s stubFinder) FindPackageForExecutable(path rootfs.Path) (*dpkg.Package, error) {
	return s.findPackage(path.Abs())
}

func unownedFilesQuery(t testing.TB) *teststub.Query {
	t.Helper()
	q := teststub.NewQuery(t)
	q.LookUpFunc = func(string) (*dpkg.Package, error) { return nil, nil }
	return q
}

type stubScanner func(string) ([]*ElfDependency, error)

func (s stubScanner) Scan(path rootfs.Path) ([]*ElfDependency, error) { return s(path.Abs()) }

// Generate an aarch64 ELF fixture, independent of the test host's executable
// format, CPU, compiler, ldd and dpkg installation. A fixture with an
// interpreter or libraries is dynamic (has PT_DYNAMIC); otherwise it is static.
func writeTestELF(t *testing.T, path string, typ elf.Type, interp bool, libraries ...string) {
	t.Helper()
	fx := elftest.Fixture{Type: typ, Machine: elf.EM_AARCH64, Entries: elftest.Needed(libraries...)}
	if interp {
		fx.Interp = "/lib/ld-linux-aarch64.so.1"
	}
	fx.Static = !interp && len(libraries) == 0
	elftest.Write(t, path, fx)
}

func readBundleTar(t *testing.T, path string) map[string]rootFSEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return readRootFSTar(t, data)
}

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
