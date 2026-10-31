package bistro

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/stretchr/testify/require"
)

// Unconfigured methods fail instead of silently returning an empty result.
type stubQuery struct {
	dpkg.Query
	baseDir string
	get     func(string) (*dpkg.Package, error)
	search  func(string) ([]*dpkg.OwnedFile, error)
}

func (s stubQuery) Get(name string) (*dpkg.Package, error) {
	if s.get != nil {
		return s.get(name)
	}
	return s.Query.Get(name)
}
func (s stubQuery) Search(path string) ([]*dpkg.OwnedFile, error) {
	if s.search != nil {
		return s.search(path)
	}
	return s.Query.Search(path)
}

type dpkgTestExecer func(string, ...string) ([]byte, error)

func (f dpkgTestExecer) Output(name string, args ...string) ([]byte, error) { return f(name, args...) }

type stubLdd func(string) (*LinkInfo, error)

func (s stubLdd) ListObjects(path string) (*LinkInfo, error) { return s(path) }

type stubFinder struct {
	trace       func(string) (*ExeTraceResult, error)
	findPackage func(string) (*dpkg.Package, error)
}

func (s stubFinder) TraceDepsForExecutable(path string) (*ExeTraceResult, error) {
	return s.trace(path)
}
func (s stubFinder) FindPackageForExecutable(path string) (*dpkg.Package, error) {
	return s.findPackage(path)
}
func unownedFilesQuery() stubQuery {
	return stubQuery{search: func(string) ([]*dpkg.OwnedFile, error) { return nil, nil }}
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

func (s stubScanner) Scan(path string) ([]*ElfDependency, error) { return s(path) }

// Generate a small ELF64 fixture, independent of the test host's executable
// format, CPU, compiler, ldd and dpkg installation. It is parsed, never executed.
func writeTestELF(t *testing.T, path string, typ elf.Type, interp bool, libraries ...string) {
	t.Helper()
	order := binary.LittleEndian
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	order.PutUint16(data[16:], uint16(typ))
	order.PutUint16(data[18:], uint16(elf.EM_AARCH64))
	order.PutUint32(data[20:], uint32(elf.EV_CURRENT))
	order.PutUint16(data[52:], 64)
	if interp {
		order.PutUint64(data[32:], 64)
		order.PutUint16(data[54:], 56)
		order.PutUint16(data[56:], 1)
		data = append(data, make([]byte, 56)...)
		order.PutUint32(data[64:], uint32(elf.PT_INTERP))
		order.PutUint64(data[72:], 120)
		loader := []byte("/lib/ld-linux-aarch64.so.1\x00")
		order.PutUint64(data[96:], uint64(len(loader)))
		data = append(data, loader...)
	}
	if len(libraries) > 0 {
		strtab := []byte{0}
		dynamic := make([]byte, 16*(len(libraries)+1))
		for i, name := range libraries {
			order.PutUint64(dynamic[i*16:], uint64(elf.DT_NEEDED))
			order.PutUint64(dynamic[i*16+8:], uint64(len(strtab)))
			strtab = append(strtab, []byte(name)...)
			strtab = append(strtab, 0)
		}
		strOffset := len(data)
		data = append(data, strtab...)
		dynOffset := len(data)
		data = append(data, dynamic...)
		sectionOffset := len(data)
		data = append(data, make([]byte, 3*64)...)
		order.PutUint64(data[40:], uint64(sectionOffset))
		order.PutUint16(data[58:], 64)
		order.PutUint16(data[60:], 3)
		strSection := data[sectionOffset+64:]
		order.PutUint32(strSection[4:], uint32(elf.SHT_STRTAB))
		order.PutUint64(strSection[24:], uint64(strOffset))
		order.PutUint64(strSection[32:], uint64(len(strtab)))
		dynSection := data[sectionOffset+128:]
		order.PutUint32(dynSection[4:], uint32(elf.SHT_DYNAMIC))
		order.PutUint64(dynSection[24:], uint64(dynOffset))
		order.PutUint64(dynSection[32:], uint64(len(dynamic)))
		order.PutUint32(dynSection[40:], 1)
		order.PutUint64(dynSection[56:], 16)
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, data, 0644))
}

func readBundleTar(t *testing.T, path string) map[string]rootFSEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return readRootFSTar(t, data)
}

// Build a real dpkg database, preserving the package pointers used by callers.
func packageFilesStub(t *testing.T, packages []*dpkg.Package, files map[string][]string) stubQuery {
	t.Helper()
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
	q := dpkg.NewQueryWithBaseDir(baseDir)
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
