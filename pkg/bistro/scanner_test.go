package bistro

import (
	"debug/elf"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/internal/teststub"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScannerELFTypes(t *testing.T) {
	for _, tt := range []struct {
		name    string
		typ     elf.Type
		interp  bool
		libs    []string
		include bool
	}{
		{name: "static executable", typ: elf.ET_EXEC, include: true},
		{name: "static PIE", typ: elf.ET_DYN, include: true},
		{name: "dynamic executable", typ: elf.ET_EXEC, interp: true, include: true},
		{name: "dynamic PIE", typ: elf.ET_DYN, interp: true, include: true},
		{name: "shared library without interpreter", typ: elf.ET_DYN, libs: []string{"libfoo.so.1"}, include: true},
		{name: "object file", typ: elf.ET_REL},
		{name: "core dump", typ: elf.ET_CORE},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(rootFSTempDir(t), "fixture")
			writeTestELF(t, path, tt.typ, tt.interp, tt.libs...)
			calls := 0
			resolver := teststub.NewResolver(t)
			resolver.ListObjectsFunc = func(got rootfs.Path) ([]*ldd.Library, error) {
				calls++
				assert.Equal(t, path, got.Abs())
				return []*ldd.Library{{Name: "missing"}}, nil
			}
			s := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: resolver})
			for range 2 {
				deps, err := s.Scan(rootfs.MustPath(path))
				require.NoError(t, err)
				if !tt.include {
					assert.Empty(t, deps)
					continue
				}
				require.Len(t, deps, 1)
				assert.Equal(t, &ElfFile{Path: rootfs.MustPath(path), Type: tt.typ}, deps[0].ElfFile)
				assert.Equal(t, []string{"missing"}, deps[0].BrokenLibs)
			}
			if tt.include {
				assert.Equal(t, 1, calls)
			} else {
				assert.Zero(t, calls, "unsupported ELF types must not invoke the resolver")
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
	s := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})})
	deps, err := s.Scan(rootfs.MustPath(dir))
	require.NoError(t, err)
	var got []string
	for _, dep := range deps {
		got = append(got, dep.ElfFile.Path.Abs())
	}
	assert.ElementsMatch(t, paths, got)
	t.Chdir(dir)
	deps, err = s.Scan(rootfs.MustPath(dir).Resolve("nested/extension.node"))
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, paths[1], deps[0].ElfFile.Path.Abs())
	for _, p := range []string{"README", "empty", "nested/object.o", "nested/object.o"} {
		deps, err := s.Scan(rootfs.MustPath(dir).Resolve(p))
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
	s := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})})
	deps, err := s.Scan(rootfs.MustPath(link))
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, file, deps[0].ElfFile.Path.Abs(), "scan the resolved regular file")
	deps, err = s.Scan(rootfs.MustPath(dirlink))
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, file, deps[0].ElfFile.Path.Abs())
	cycle := filepath.Join(dir, "cycle")
	require.NoError(t, os.Symlink("cycle", cycle))
	_, err = s.Scan(rootfs.MustPath(cycle))
	require.ErrorContains(t, err, "circular symlink detected")
}

func TestScannerFailures(t *testing.T) {
	dir := rootFSTempDir(t)
	s := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})}).(*scanner)
	missing := filepath.Join(dir, "missing")
	_, err := s.Scan(rootfs.MustPath(missing))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, _, err = s.handleFile(rootfs.MustPath(missing))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.Symlink("missing", filepath.Join(dir, "broken")))
	_, err = s.Scan(rootfs.MustPath(dir))
	require.ErrorIs(t, err, os.ErrNotExist)
	nested := rootFSTempDir(t)
	require.NoError(t, os.Mkdir(filepath.Join(nested, "child"), 0755))
	require.NoError(t, os.Symlink("missing", filepath.Join(nested, "child", "broken")))
	_, err = s.Scan(rootfs.MustPath(nested))
	require.ErrorIs(t, err, os.ErrNotExist, "propagate errors from nested directories")
	file := filepath.Join(rootFSTempDir(t), "program")
	writeTestELF(t, file, elf.ET_DYN, true)
	failure := errors.New("trace failed")
	calls := 0
	s.finder = stubFinder{findPackage: func(string) (*dpkg.Package, error) { return nil, errPackageNotFound }, trace: func(string) (*ExeTraceResult, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return &ExeTraceResult{BrokenLibs: []string{"missing"}}, nil
	}}
	_, err = s.Scan(rootfs.MustPath(file))
	require.ErrorIs(t, err, failure)
	deps, err := s.Scan(rootfs.MustPath(file))
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, []string{"missing"}, deps[0].BrokenLibs)
	assert.Equal(t, 2, calls, "failed traces must be retried")
	s.finder = stubFinder{findPackage: func(string) (*dpkg.Package, error) { return nil, errPackageNotFound }, trace: func(string) (*ExeTraceResult, error) { return nil, failure }}
	s.depsCache = make(map[rootfs.Path]*ElfDependency)
	_, err = s.Scan(rootfs.MustPath(filepath.Dir(file)))
	require.ErrorIs(t, err, failure)
}

func TestScannerDependencies(t *testing.T) {
	path := filepath.Join(rootFSTempDir(t), "program")
	writeTestELF(t, path, elf.ET_EXEC, true)
	want := []*ExeDependency{{Library: &ldd.Library{Name: "libfoo.so", Path: rootfs.MustPath("/lib/libfoo.so")}, Package: testPackage(t, "foo", "", "", "")}}
	s := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})}).(*scanner)
	s.finder = stubFinder{findPackage: func(string) (*dpkg.Package, error) { return nil, errPackageNotFound }, trace: func(got string) (*ExeTraceResult, error) {
		assert.Equal(t, path, got)
		return &ExeTraceResult{Dependencies: want}, nil
	}}
	got, err := s.Scan(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, want, got[0].Dependencies)
}

func TestScannerRetriesFailedOwnerLookup(t *testing.T) {
	path := filepath.Join(rootFSTempDir(t), "static")
	writeTestELF(t, path, elf.ET_EXEC, false)
	failure := errors.New("owner lookup failed")
	calls := 0
	d := teststub.NewQuery(t)
	d.LookUpFunc = func(got string) (*dpkg.Package, error) {
		calls++
		assert.Equal(t, path, got)
		if calls == 1 {
			return nil, failure
		}
		return nil, nil
	}
	s := NewScanner(DI{FS: os.DirFS("/"), Query: d, Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})})
	_, err := s.Scan(rootfs.MustPath(path))
	require.ErrorIs(t, err, failure)
	for range 2 {
		deps, err := s.Scan(rootfs.MustPath(path))
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
	s := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})})
	_, err := s.Scan(rootfs.MustPath(path))
	require.EqualError(t, err, "[ldd/readElf] malformed ELF file: "+path+": [ldd/readDynTable] truncated PT_DYNAMIC")
}

func TestScannerExclusions(t *testing.T) {
	dir := rootFSTempDir(t)
	excluded := filepath.Join(dir, "cache")
	kept := filepath.Join(dir, "cache-old", "program")
	ignoredFile := filepath.Join(dir, "ignored")
	writeTestELF(t, kept, elf.ET_EXEC, false)
	writeTestELF(t, ignoredFile, elf.ET_EXEC, false)
	require.NoError(t, os.MkdirAll(excluded, 0755))
	require.NoError(t, os.Symlink("missing", filepath.Join(excluded, "broken")))
	di := DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t)}
	di.Ldd = ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: di.FS})
	s := NewScanner(di)
	excludes := rootfs.MustPaths(excluded, ignoredFile)
	s.SetExcludes(rootfs.NewPathSet(excludes))
	deps, err := s.Scan(rootfs.MustPath(dir))
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, kept, deps[0].ElfFile.Path.Abs())

	for _, path := range []string{excluded, filepath.Join(excluded, "broken"), ignoredFile} {
		deps, err := s.Scan(rootfs.MustPath(path))
		require.NoError(t, err)
		assert.Empty(t, deps)
	}
}

func TestScannerDeduplicatesResolvedELFFiles(t *testing.T) {
	for _, aliasName := range []string{"alias", "z-alias"} {
		t.Run(aliasName, func(t *testing.T) {
			dir := rootFSTempDir(t)
			program := filepath.Join(dir, "program")
			writeTestELF(t, program, elf.ET_EXEC, false)
			require.NoError(t, os.Symlink("program", filepath.Join(dir, aliasName)))
			calls := 0
			resolver := teststub.NewResolver(t)
			resolver.ListObjectsFunc = func(p rootfs.Path) ([]*ldd.Library, error) {
				calls++
				require.Equal(t, program, p.Abs())
				return nil, nil
			}
			scanner := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: resolver})
			for range 2 {
				deps, err := scanner.Scan(rootfs.MustPath(dir))
				require.NoError(t, err)
				require.Len(t, deps, 1, "alias ordering must not change the result count")
				require.Equal(t, program, deps[0].ElfFile.Path.Abs())
			}
			require.Equal(t, 1, calls, "dependency results should remain cached across scans")
		})
	}
}

func TestScannerTraversesNestedDirectoryAliases(t *testing.T) {
	dir := rootFSTempDir(t)
	tree := filepath.Join(dir, "tree")
	outside := filepath.Join(dir, "outside")
	program := filepath.Join(outside, "program")
	writeTestELF(t, program, elf.ET_EXEC, false)
	require.NoError(t, os.Mkdir(tree, 0755))
	require.NoError(t, os.Symlink("../outside", filepath.Join(tree, "plugins")))
	require.NoError(t, os.Symlink("../tree", filepath.Join(outside, "back")))
	scanner := NewScanner(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})})
	deps, err := scanner.Scan(rootfs.MustPath(tree))
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, program, deps[0].ElfFile.Path.Abs())
	scanner.SetExcludes(rootfs.NewPathSet(rootfs.MustPaths(outside)))
	deps, err = scanner.Scan(rootfs.MustPath(tree))
	require.NoError(t, err)
	require.Empty(t, deps, "excluded directory targets must not be scanned")
}

// Fail immediately if scanning tries to open a special file, without a blocking FIFO fixture.
type specialFileGuardFS struct {
	fs.FS
	opens int
}

func (f *specialFileGuardFS) Lstat(name string) (fs.FileInfo, error) { return fs.Lstat(f.FS, name) }
func (f *specialFileGuardFS) ReadLink(name string) (string, error)   { return fs.ReadLink(f.FS, name) }
func (f *specialFileGuardFS) Open(name string) (fs.File, error) {
	info, err := fs.Stat(f.FS, name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		f.opens++
		return nil, errors.New("special file must not be opened for ELF scanning")
	}
	return f.FS.Open(name)
}

func TestScannerSkipsSpecialFilesBeforeOpening(t *testing.T) {
	for _, mode := range []fs.FileMode{fs.ModeNamedPipe, fs.ModeSocket, fs.ModeDevice | fs.ModeCharDevice} {
		t.Run(mode.String(), func(t *testing.T) {
			fss := &specialFileGuardFS{FS: fstest.MapFS{
				"tree/special": {Mode: mode | 0600},
				"alias":        {Mode: fs.ModeSymlink | 0777, Data: []byte("tree/special")},
			}}
			scanner := NewScanner(DI{FS: fss})
			for _, p := range []string{"/tree/special", "/alias", "/tree"} {
				deps, err := scanner.Scan(rootfs.MustPath(p))
				require.NoError(t, err)
				require.Empty(t, deps)
			}
			require.Zero(t, fss.opens)
		})
	}
}
