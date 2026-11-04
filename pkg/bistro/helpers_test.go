package bistro

import (
	"archive/tar"
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/internal/testfs"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

// Unconfigured methods fail instead of silently returning an empty result.
type stubQuery struct {
	dpkg.Query
	baseDir string
	get     func(string) (*dpkg.Package, error)
	lookup  func(string) (*dpkg.Package, error)
}

func (s stubQuery) Get(name string) (*dpkg.Package, error) {
	if s.get != nil {
		return s.get(name)
	}
	return s.Query.Get(name)
}
func (s stubQuery) LookUp(path string) (*dpkg.Package, error) {
	if s.lookup != nil {
		return s.lookup(path)
	}
	return s.Query.LookUp(path)
}

type dpkgTestExecer func(string, ...string) ([]byte, error)

func (f dpkgTestExecer) Output(name string, args ...string) ([]byte, error) { return f(name, args...) }

type stubLdd func(string) ([]*ldd.Library, error)

func (s stubLdd) Load() error { return nil }

func (s stubLdd) ListObjects(path rootfs.Path) ([]*ldd.Library, error) { return s(path.Abs()) }

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

// A dpkg database containing only the always-bundled base-files package.
func baseFilesQuery(t *testing.T) stubQuery {
	t.Helper()
	return packageFilesStub(t, nil, nil)
}

func unownedFilesQuery() stubQuery {
	return stubQuery{lookup: func(string) (*dpkg.Package, error) { return nil, nil }}
}

// unownedFilesQuery for bundling: files are unowned, but base-files exists.
func unownedBaseFilesQuery(t *testing.T) stubQuery {
	t.Helper()
	q := baseFilesQuery(t)
	q.lookup = func(string) (*dpkg.Package, error) { return nil, nil }
	return q
}

// DI backed by baseFilesQuery, so bundling never reads the host's dpkg database.
func baseFilesDI(t *testing.T) DI {
	t.Helper()
	q := baseFilesQuery(t)
	return DI{FS: testFS(q), Query: q}
}

func testPackage(t *testing.T, binaryName, version, arch, status string) *dpkg.Package {
	t.Helper()
	name, qualifier, qualified := strings.Cut(binaryName, ":")
	if arch == "" {
		arch = "all"
		if qualified {
			arch = qualifier
		}
	}
	if status == "" {
		status = dpkg.StatusInstalled
	}
	content := fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: %s\nStatus: install ok %s\n", name, version, arch, status)
	if qualified {
		content += "Multi-Arch: same\n"
	}
	packages, err := dpkg.ParseStatus(content)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	return packages[0]
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

// Build a real dpkg database, preserving the package pointers used by callers.
// base-files is added unless provided, because every bundle requires it.
func packageFilesStub(t *testing.T, packages []*dpkg.Package, files map[string][]string) stubQuery {
	t.Helper()
	if !slices.ContainsFunc(packages, func(p *dpkg.Package) bool { return p.Name() == BaseFilesPackage }) {
		packages = append(slices.Clone(packages), testPackage(t, BaseFilesPackage, "", "", ""))
	}
	baseDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(baseDir, "info"), 0755))
	var status strings.Builder
	for _, pkg := range packages {
		status.WriteString(pkg.String())
		status.WriteString("\n")
		name := pkg.NameWithArch()
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info", name+".list"), []byte(strings.Join(files[name], "\n")), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info", name+".md5sums"), []byte("d41d8cd98f00b204e9800998ecf8427e  usr/share/"+name+"\n"), 0644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "status"), []byte(status.String()), 0644))
	q := dpkg.NewQuery(testfs.Mount(os.DirFS("/"), "var/lib/dpkg", os.DirFS(baseDir)))
	require.NoError(t, q.Load())
	for _, pkg := range packages {
		loaded, err := q.Get(pkg.Name())
		require.NoError(t, err)
		*pkg = *loaded
	}
	return stubQuery{Query: q, baseDir: baseDir, get: func(name string) (*dpkg.Package, error) {
		for _, pkg := range packages {
			if pkg.NameWithArch() == name || pkg.Name() == name {
				return pkg, nil
			}
		}
		return nil, fmt.Errorf("package not found: %s", name)
	}}
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

func testFS(q dpkg.Query) fs.FS {
	if stub, ok := q.(stubQuery); ok && stub.baseDir != "" {
		return testfs.Mount(os.DirFS("/"), "var/lib/dpkg", os.DirFS(stub.baseDir))
	}
	return os.DirFS("/")
}

func testQuery(baseDir string) stubQuery {
	q := stubQuery{baseDir: baseDir}
	q.Query = dpkg.NewQuery(testFS(q))
	return q
}
