package bistro

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Unconfigured methods fail instead of silently returning an empty result.
type stubDpkg struct {
	listPackages func(...string) ([]*Package, error)
	listFiles    func(string) ([]string, error)
	search       func(string) ([]*OwnedFile, error)
	status       func(string) ([]byte, error)
	md5sums      func(string) ([]byte, error)
}

func (s stubDpkg) ListPackages(names ...string) ([]*Package, error) { return s.listPackages(names...) }
func (s stubDpkg) ListFiles(name string) ([]string, error)          { return s.listFiles(name) }
func (s stubDpkg) Search(path string) ([]*OwnedFile, error)         { return s.search(path) }
func (s stubDpkg) PackageStatus(name string) ([]byte, error)        { return s.status(name) }
func (s stubDpkg) PackageMd5Sums(name string) ([]byte, error)       { return s.md5sums(name) }

type stubLdd func(string) (*LinkInfo, error)

func (s stubLdd) ListObjects(path string) (*LinkInfo, error) { return s(path) }

type stubFinder func(string) (*ExeTraceResult, error)

func (s stubFinder) TraceDepsForExecutable(path string) (*ExeTraceResult, error) { return s(path) }

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

func packageFilesStub(t *testing.T, packages []*Package, files map[string][]string) stubDpkg {
	t.Helper()
	return stubDpkg{
		listPackages: func(names ...string) ([]*Package, error) { return packages, nil },
		listFiles:    func(name string) ([]string, error) { return files[name], nil },
		status: func(name string) ([]byte, error) {
			return []byte("Package: " + name + "\nStatus: install ok installed\n"), nil
		},
		md5sums: func(name string) ([]byte, error) { return []byte("checksum " + name + "\n"), nil },
	}
}
