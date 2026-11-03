package bistro

import (
	"debug/elf"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScannerELFTypes(t *testing.T) {
	for _, tt := range []struct {
		name             string
		typ              elf.Type
		interp           bool
		libs             []string
		include, dynamic bool
	}{
		{name: "static executable", typ: elf.ET_EXEC, include: true},
		{name: "static PIE", typ: elf.ET_DYN, include: true},
		{name: "dynamic executable", typ: elf.ET_EXEC, interp: true, include: true, dynamic: true},
		{name: "dynamic PIE", typ: elf.ET_DYN, interp: true, include: true, dynamic: true},
		{name: "shared library without interpreter", typ: elf.ET_DYN, libs: []string{"libfoo.so.1"}, include: true, dynamic: true},
		{name: "object file", typ: elf.ET_REL},
		{name: "core dump", typ: elf.ET_CORE},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(rootFSTempDir(t), "fixture")
			writeTestELF(t, path, tt.typ, tt.interp, tt.libs...)
			calls := 0
			s := NewScanner(testDI(DI{Query: unownedFilesQuery(), Ldd: stubLdd(func(got string) ([]*ldd.Library, error) {
				calls++
				assert.Equal(t, path, got)
				return []*ldd.Library{{Name: "missing"}}, nil
			})}))
			for range 2 {
				deps, err := s.Scan(path)
				require.NoError(t, err)
				if !tt.include {
					assert.Empty(t, deps)
					continue
				}
				require.Len(t, deps, 1)
				assert.Equal(t, &ElfFile{Path: rootfs.NewPath(path), Type: tt.typ, NeedsLdd: tt.dynamic}, deps[0].ElfFile)
				if tt.dynamic {
					assert.Equal(t, []string{"missing"}, deps[0].BrokenLibs)
				} else {
					assert.Empty(t, deps[0].BrokenLibs)
				}
			}
			if tt.dynamic {
				assert.Equal(t, 1, calls)
			} else {
				assert.Zero(t, calls, "static ELF must not invoke ldd")
			}
		})
	}
}

func TestScannerRecursiveAndRelativePaths(t *testing.T) {
	dir := rootFSTempDir(t)
	paths := []string{filepath.Join(dir, "program"), filepath.Join(dir, "nested", "extension.node"), filepath.Join(dir, "nested", "library.so")}
	for _, p := range paths {
		writeTestELF(t, p, elf.ET_DYN, false)
	}
	writeTestELF(t, filepath.Join(dir, "nested", "object.o"), elf.ET_REL, false)
	writeRootFSFile(t, filepath.Join(dir, "README"), "plain text", 0644)
	writeRootFSFile(t, filepath.Join(dir, "empty"), "", 0644)
	s := NewScanner(testDI(DI{Query: unownedFilesQuery()}))
	deps, err := s.Scan(dir)
	require.NoError(t, err)
	var got []string
	for _, dep := range deps {
		got = append(got, dep.ElfFile.Path.Abs())
	}
	assert.ElementsMatch(t, paths, got)
	t.Chdir(dir)
	di := testDI(DI{Query: unownedFilesQuery()})
	di.FS = os.DirFS(dir)
	di.SymLink = rootfs.NewSymLinkResolver(di.FS)
	s = NewScanner(di)
	deps, err = s.Scan("nested/extension.node")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, rootfs.NewPath("nested/extension.node"), deps[0].ElfFile.Path)
	for _, p := range []string{"README", "empty", "nested/object.o", "nested/object.o"} {
		deps, err := s.Scan(p)
		require.NoError(t, err)
		assert.Empty(t, deps)
	}
}

func TestScannerSymlinks(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "nested", "program")
	writeTestELF(t, file, elf.ET_EXEC, false)
	link := filepath.Join(dir, "alias")
	require.NoError(t, os.Symlink("nested/program", link))
	dirlink := filepath.Join(dir, "directory")
	require.NoError(t, os.Symlink("nested", dirlink))
	s := NewScanner(testDI(DI{Query: unownedFilesQuery()}))
	deps, err := s.Scan(link)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, link, deps[0].ElfFile.Path.Abs(), "retain input alias for bundling")
	deps, err = s.Scan(dirlink)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, filepath.Join(dirlink, "program"), deps[0].ElfFile.Path.Abs())
	cycle := filepath.Join(dir, "cycle")
	require.NoError(t, os.Symlink("cycle", cycle))
	_, err = s.Scan(cycle)
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	assert.Equal(t, "stat", pathErr.Op)
	assert.Equal(t, "cycle", filepath.Base(pathErr.Path))
	assert.ErrorIs(t, err, syscall.ELOOP)
}

func TestScannerFailures(t *testing.T) {
	dir := rootFSTempDir(t)
	s := NewScanner(testDI(DI{Query: unownedFilesQuery()})).(*scanner)
	missing := filepath.Join(dir, "missing")
	_, err := s.Scan(missing)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, _, err = s.handleFile(rootfs.NewPath(missing))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = s.scanDir(rootfs.NewPath(missing))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.Symlink("missing", filepath.Join(dir, "broken")))
	_, err = s.Scan(dir)
	require.ErrorIs(t, err, os.ErrNotExist)
	nested := rootFSTempDir(t)
	require.NoError(t, os.Mkdir(filepath.Join(nested, "child"), 0755))
	require.NoError(t, os.Symlink("missing", filepath.Join(nested, "child", "broken")))
	_, err = s.Scan(nested)
	require.ErrorIs(t, err, os.ErrNotExist, "propagate errors from nested directories")
	file := filepath.Join(rootFSTempDir(t), "program")
	writeTestELF(t, file, elf.ET_DYN, true)
	failure := errors.New("trace failed")
	calls := 0
	s.finder = stubFinder{findPackage: func(string) (dpkg.Package, error) { return nil, errPackageNotFound }, trace: func(string) (*ExeTraceResult, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return &ExeTraceResult{BrokenLibs: []string{"missing"}}, nil
	}}
	_, err = s.Scan(file)
	require.ErrorIs(t, err, failure)
	deps, err := s.Scan(file)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, []string{"missing"}, deps[0].BrokenLibs)
	assert.Equal(t, 2, calls, "failed traces must be retried")
	s.finder = stubFinder{findPackage: func(string) (dpkg.Package, error) { return nil, errPackageNotFound }, trace: func(string) (*ExeTraceResult, error) { return nil, failure }}
	s.depsCache.Clear()
	_, err = s.Scan(filepath.Dir(file))
	require.ErrorIs(t, err, failure)
}

func TestScannerDependencies(t *testing.T) {
	path := filepath.Join(rootFSTempDir(t), "program")
	writeTestELF(t, path, elf.ET_EXEC, true)
	want := []*ExeDependency{{Library: &ldd.Library{Name: "libfoo.so", Path: rootfs.NewPath("/lib/libfoo.so")}, Package: testPackage(t, "foo", "", "", "")}}
	s := NewScanner(testDI(DI{Query: unownedFilesQuery()})).(*scanner)
	s.finder = stubFinder{findPackage: func(string) (dpkg.Package, error) { return nil, errPackageNotFound }, trace: func(got string) (*ExeTraceResult, error) {
		assert.Equal(t, path, got)
		return &ExeTraceResult{Dependencies: want}, nil
	}}
	got, err := s.Scan(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, want, got[0].Dependencies)
}

func TestScannerRetriesFailedOwnerLookup(t *testing.T) {
	path := filepath.Join(rootFSTempDir(t), "static")
	writeTestELF(t, path, elf.ET_EXEC, false)
	failure := errors.New("owner lookup failed")
	calls := 0
	d := stubQuery{lookup: func(got string) (dpkg.Package, error) {
		calls++
		assert.Equal(t, path, got)
		if calls == 1 {
			return nil, failure
		}
		return nil, nil
	}}
	s := NewScanner(testDI(DI{Query: d}))
	_, err := s.Scan(path)
	require.ErrorIs(t, err, failure)
	for range 2 {
		deps, err := s.Scan(path)
		require.NoError(t, err)
		require.Len(t, deps, 1)
		assert.Nil(t, deps[0].Package)
	}
	assert.Equal(t, 2, calls, "retry failed lookups and cache successful scans of unowned files")
}

func TestScannerMalformedDynamicSegment(t *testing.T) {
	path := filepath.Join(rootFSTempDir(t), "library.so")
	elftest.Write(t, path, elftest.Fixture{
		Machine: elf.EM_AARCH64,
		Entries: elftest.Needed("libfoo.so"),
		// Extend PT_DYNAMIC well past the end of file.
		Progs: func(p []elf.Prog64) { elftest.Prog(p, elf.PT_DYNAMIC).Filesz += 1 << 20 },
	})
	s := NewScanner(testDI(DI{Query: unownedFilesQuery(), Ldd: ldd.NewResolver(os.DirFS("/"), ldd.ResolverOptions{})}))
	_, err := s.Scan(path)
	require.EqualError(t, err, "[ldd/readElf] malformed ELF file: "+rootfs.NewPath(path).Path()+": [ldd/readDynTable] truncated PT_DYNAMIC")
}
