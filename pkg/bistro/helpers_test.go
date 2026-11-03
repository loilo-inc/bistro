package bistro

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/require"
)

// Unconfigured methods fail instead of silently returning an empty result.
type stubQuery struct {
	dpkg.Query
	baseDir string
	get     func(string) (dpkg.Package, error)
	lookup  func(string) (dpkg.Package, error)
}

func (s stubQuery) Get(name string) (dpkg.Package, error) {
	if s.get != nil {
		return s.get(name)
	}
	return s.Query.Get(name)
}
func (s stubQuery) LookUp(path string) (dpkg.Package, error) {
	if s.lookup != nil {
		return s.lookup(path)
	}
	return s.Query.LookUp(path)
}

type stubLdd func(string) ([]*ldd.Library, error)

func (s stubLdd) Load() error { return nil }

func (s stubLdd) ListObjects(path string) ([]*ldd.Library, error) { return s(path) }

type stubFinder struct {
	trace       func(string) (*ExeTraceResult, error)
	findPackage func(string) (dpkg.Package, error)
}

func (s stubFinder) TraceDepsForExecutable(path string) (*ExeTraceResult, error) {
	return s.trace(path)
}
func (s stubFinder) FindPackageForExecutable(path string) (dpkg.Package, error) {
	return s.findPackage(path)
}
func unownedFilesQuery() stubQuery {
	return stubQuery{lookup: func(string) (dpkg.Package, error) { return nil, nil }}
}

func testPackage(t *testing.T, binaryName, version, arch, status string) dpkg.Package {
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
	return &testDpkgPackage{Package: packages[0]}
}

type stubScanner func(string) ([]*ElfDependency, error)

func (s stubScanner) Scan(path string) ([]*ElfDependency, error) { return s(path) }

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

// Build a real dpkg database and update the package wrappers used by callers.
func packageFilesStub(t *testing.T, packages []dpkg.Package, files map[string][]string) stubQuery {
	t.Helper()
	root := t.TempDir()
	baseDir := filepath.Join(root, dpkg.DefaultBaseDir)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "info"), 0755))
	var status strings.Builder
	for _, pkg := range packages {
		status.WriteString(pkg.String())
		status.WriteString("\n")
		name := pkg.NameWithArch()
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info", name+".list"), []byte(strings.Join(files[name], "\n")), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info", name+".md5sums"), []byte("d41d8cd98f00b204e9800998ecf8427e  usr/share/"+name+"\n"), 0644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "status"), []byte(status.String()), 0644))
	q := dpkg.NewQuery(os.DirFS(root))
	require.NoError(t, q.Load())
	for _, pkg := range packages {
		loaded, err := q.Get(pkg.Name())
		require.NoError(t, err)
		pkg.(*testDpkgPackage).Package = loaded
	}
	return stubQuery{Query: q, baseDir: baseDir, get: func(name string) (dpkg.Package, error) {
		for _, pkg := range packages {
			if pkg.NameWithArch() == name || pkg.Name() == name {
				return pkg, nil
			}
		}
		return nil, fmt.Errorf("package not found: %s", name)
	}}
}

type testDpkgPackage struct{ dpkg.Package }

func testDI(di DI) DI {
	di.FS = os.DirFS("/")
	di.SymLink = rootfs.NewSymLinkResolver(di.FS)
	return di
}
