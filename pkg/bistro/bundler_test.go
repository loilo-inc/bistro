package bistro

import (
	"archive/tar"
	"debug/elf"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/internal/teststub"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundlerMergesInputs(t *testing.T) {
	dir := rootFSTempDir(t)
	file, scan := filepath.Join(dir, "program"), filepath.Join(dir, "extension.so")
	owned := filepath.Join(dir, "library.so")
	unowned := filepath.Join(dir, "unowned.so")
	writeRootFSFile(t, file, "program", 0755)
	writeRootFSFile(t, scan, "scan target", 0644)
	writeRootFSFile(t, owned, "shared library", 0644)
	writeRootFSFile(t, unowned, "unowned library", 0644)
	pkg := testPackage(t, "library:arm64", "", "", dpkg.StatusInstalled)
	other := testPackage(t, "other:arm64", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg, other}, map[string][]string{pkg.NameWithArch(): {owned}})
	var requested []string
	get := d.query.GetFunc
	d.query.GetFunc = func(name string) (*dpkg.Package, error) {
		requested = append(requested, name)
		return get(name)
	}
	b := newTestBundler(DI{FS: d.fs, Query: d.query})
	var scanned []string
	b.scanner = stubScanner(func(path string) ([]*ElfDependency, error) {
		scanned = append(scanned, path)
		return []*ElfDependency{{
			ElfFile: &ElfFile{Path: rootfs.MustPath(path)},
			Dependencies: []*ExeDependency{
				{Library: &ldd.Library{Path: rootfs.MustPath(unowned)}},
				{Library: &ldd.Library{Path: rootfs.MustPath(owned)}, Package: pkg},
				{Library: &ldd.Library{Path: rootfs.MustPath(owned)}, Package: testPackage(t, pkg.NameWithArch(), pkg.Version(), pkg.Architecture(), pkg.Status().Current)},
			},
		}}, nil
	})
	out := filepath.Join(dir, "bundle.tar")
	b.BundleInput = BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(file, file, scan), Packages: []string{pkg.NameWithArch(), other.NameWithArch()}}
	require.NoError(t, b.bundle())
	assert.Equal(t, []string{file, file, scan}, scanned)
	assert.Equal(t, []string{pkg.NameWithArch(), other.NameWithArch()}, requested)
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(file))
	assert.Contains(t, entries, rootFSArchiveName(owned))
	assert.Equal(t, "unowned library", entries[rootFSArchiveName(unowned)].body)
	assert.Contains(t, entries, rootFSArchiveName(scan))
	assert.Contains(t, entries, "var/lib/dpkg/status.d/library")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/other")
}

func TestBundlerInterpreterWithoutDynamicSegment(t *testing.T) {
	for _, typ := range []elf.Type{elf.ET_EXEC, elf.ET_DYN} {
		t.Run(typ.String(), func(t *testing.T) {
			dir := rootFSTempDir(t)
			app, loader := filepath.Join(dir, "app"), filepath.Join(dir, "loader")
			elftest.Write(t, app, elftest.Fixture{Type: typ, Static: true, Interp: loader})
			elftest.Write(t, loader, elftest.Fixture{Static: true})
			di := DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t)}
			di.Ldd = ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: di.FS})
			deps, err := NewScanner(di).Scan(rootfs.MustPath(app))
			require.NoError(t, err)
			require.Len(t, deps, 1)
			require.Len(t, deps[0].Dependencies, 1)
			assert.Equal(t, loader, deps[0].Dependencies[0].Library.Path.Abs())
			assert.Empty(t, deps[0].BrokenLibs)
			out := filepath.Join(dir, "bundle.tar")
			require.NoError(t, Bundle(di, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(app)}))
			entries := readBundleTar(t, out)
			for _, path := range []string{app, loader} {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, string(data), entries[rootFSArchiveName(path)].body)
			}
		})
	}
}

func TestBundlerPackageOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "font.ttf")
	writeRootFSFile(t, file, "font data", 0644)
	pkg := testPackage(t, "fonts", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {file}})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, Bundle(DI{FS: d.fs, Query: d.query}, BundleInput{Out: rootfs.MustPath(out), Packages: []string{"fonts"}}))
	assert.Equal(t, "font data", readBundleTar(t, out)[rootFSArchiveName(file)].body)
}

func TestBundlerNoScan(t *testing.T) {
	dir := rootFSTempDir(t)
	program := filepath.Join(dir, "program")
	tree := filepath.Join(dir, "tree")
	extension := filepath.Join(tree, "extension.so")
	font := filepath.Join(dir, "font.ttf")
	writeTestELF(t, program, elf.ET_EXEC, true)
	writeTestELF(t, extension, elf.ET_DYN, false, "missing.so")
	writeRootFSFile(t, font, "font data", 0644)
	pkg := testPackage(t, "fonts", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {font}})
	b := newTestBundler(DI{FS: d.fs, Query: d.query})
	b.scanner = stubScanner(func(string) ([]*ElfDependency, error) {
		t.Fatal("scanner must not be called when NoScan is enabled")
		return nil, nil
	})
	out := filepath.Join(dir, "bundle.tar")
	b.BundleInput = BundleInput{
		Out: rootfs.MustPath(out), Files: rootfs.MustPaths(program, tree),
		Packages: []string{pkg.NameWithArch()}, NoScan: true,
	}

	require.NoError(t, b.bundle())
	entries := readBundleTar(t, out)
	for _, file := range []string{program, extension} {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, string(data), entries[rootFSArchiveName(file)].body)
	}
	assert.Equal(t, "font data", entries[rootFSArchiveName(font)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/fonts")
}

func TestBundlerCopyExclusions(t *testing.T) {
	dir := rootFSTempDir(t)
	source := filepath.Join(dir, "source")
	excluded := filepath.Join(source, "cache")
	ignoredFile := filepath.Join(source, "ignored")
	kept := filepath.Join(source, "cache-old", "data")
	child := filepath.Join(excluded, "data")
	for _, file := range []string{ignoredFile, kept, child} {
		writeRootFSFile(t, file, "content", 0644)
	}
	out := filepath.Join(dir, "bundle.tar")
	di := DI{FS: os.DirFS("/")}
	require.NoError(t, Bundle(di, BundleInput{
		Out: rootfs.MustPath(out), Files: rootfs.MustPaths(source, child, ignoredFile),
		Excludes: rootfs.MustPaths(excluded, ignoredFile),
	}))
	entries := readBundleTar(t, out)
	assert.Equal(t, "content", entries[rootFSArchiveName(kept)].body)
	for _, file := range []string{excluded, child, ignoredFile} {
		assert.NotContains(t, entries, rootFSArchiveName(file))
	}
}

func TestBundlerWithUnselectedMultiarchPackages(t *testing.T) {
	dir := rootFSTempDir(t)
	baseDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(baseDir, "info"), 0755))
	status := "Package: library\nArchitecture: amd64\nMulti-Arch: same\nStatus: install ok installed\n\n" +
		"Package: library\nArchitecture: arm64\nMulti-Arch: same\nStatus: install ok installed\n\n" +
		"Package: fonts\nArchitecture: all\nStatus: install ok installed\n"
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "status"), []byte(status), 0644))
	file := filepath.Join(dir, "font.ttf")
	writeRootFSFile(t, file, "font data", 0644)
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info/fonts.list"), []byte(file+"\n"), 0644))
	q := newPackageDBFixture(t, baseDir)
	di := DI{FS: q.fs, Query: q.query}
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, Bundle(di, BundleInput{Out: rootfs.MustPath(out), Packages: []string{"fonts"}}))
	entries := readBundleTar(t, out)
	assert.Equal(t, "font data", entries[rootFSArchiveName(file)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/fonts")
	assert.NotContains(t, entries, "var/lib/dpkg/status.d/library")

	// Selecting both architectures still conflicts in distroless metadata.
	err := Bundle(di, BundleInput{Out: rootfs.MustPath(out), Packages: []string{"library:amd64", "library:arm64"}})
	require.ErrorContains(t, err, "package metadata name conflict")
	assert.Equal(t, "font data", readBundleTar(t, out)[rootFSArchiveName(file)].body)
}

func TestBundlerCollectsTargetPackage(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		for _, symlink := range []bool{false, true} {
			name := "static"
			if dynamic {
				name = "dynamic"
			}
			if symlink {
				name += " symlink"
			}
			t.Run(name, func(t *testing.T) {
				dir := rootFSTempDir(t)
				program, data := filepath.Join(dir, "program"), filepath.Join(dir, "data")
				writeTestELF(t, program, elf.ET_EXEC, dynamic)
				writeRootFSFile(t, data, "application data", 0644)
				target := program
				if symlink {
					target = filepath.Join(dir, "alias")
					require.NoError(t, os.Symlink("program", target))
				}
				pkg := testPackage(t, "app", "", "", dpkg.StatusInstalled)
				d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {program, data}})
				traces := 0
				l := teststub.NewResolver(t)
				l.ListObjectsFunc = func(path rootfs.Path) ([]*ldd.Library, error) {
					traces++
					assert.Equal(t, target, path.Abs())
					return nil, nil
				}
				out := filepath.Join(dir, "bundle.tar")
				require.NoError(t, Bundle(DI{FS: d.fs, Query: d.query, Ldd: l}, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(target)}))
				entries := readBundleTar(t, out)
				assert.Contains(t, entries, rootFSArchiveName(program))
				assert.Equal(t, "application data", entries[rootFSArchiveName(data)].body)
				assert.Contains(t, entries, "var/lib/dpkg/status.d/app")
				assert.Contains(t, entries, "var/lib/dpkg/status.d/app.md5sums")
				if symlink {
					assert.Equal(t, byte(tar.TypeSymlink), entries[rootFSArchiveName(target)].header.Typeflag)
				}
				assert.Equal(t, 1, traces)
			})
		}
	}
}

func TestBundlerStaticFile(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "static")
	writeTestELF(t, file, elf.ET_EXEC, false)
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, Bundle(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: os.DirFS("/")})}, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(file)}))
	assert.Contains(t, readBundleTar(t, out), rootFSArchiveName(file))
}

func TestBundlerScansELFAndCollectsOwningPackages(t *testing.T) {
	dir := rootFSTempDir(t)
	program := filepath.Join(dir, "program")
	extension := filepath.Join(dir, "extension.node")
	library := filepath.Join(dir, "libfoo.so")
	writeTestELF(t, program, elf.ET_EXEC, true)
	writeTestELF(t, extension, elf.ET_DYN, false, "libfoo.so")
	writeRootFSFile(t, library, "runtime library", 0644)
	pkg := testPackage(t, "libfoo:arm64", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {library}})
	lookups, traces := 0, 0
	d.query.LookUpFunc = func(path string) (*dpkg.Package, error) {
		lookups++
		if path != library {
			assert.Contains(t, []string{program, extension}, path)
			return nil, nil
		}
		return pkg, nil
	}
	l := teststub.NewResolver(t)
	l.ListObjectsFunc = func(path rootfs.Path) ([]*ldd.Library, error) {
		traces++
		assert.Contains(t, []string{program, extension}, path.Abs())
		return []*ldd.Library{{Name: "libfoo.so", Path: rootfs.MustPath(library)}}, nil
	}
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, Bundle(DI{FS: d.fs, Query: d.query, Ldd: l}, BundleInput{
		Out: rootfs.MustPath(out), Files: rootfs.MustPaths(program, extension, program),
	}))
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(program))
	assert.Contains(t, entries, rootFSArchiveName(extension))
	assert.Equal(t, "runtime library", entries[rootFSArchiveName(library)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/libfoo")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/libfoo.md5sums")
	assert.Equal(t, 2, traces)
	assert.Equal(t, 4, lookups)
}

func TestBundlerFailures(t *testing.T) {
	for _, stage := range []string{"file scan", "directory scan", "packages", "broken library", "uninstalled explicit package", "uninstalled target package", "uninstalled dependency", "baker"} {
		t.Run(stage, func(t *testing.T) {
			dir := rootFSTempDir(t)
			out := filepath.Join(dir, "bundle.tar")
			writeRootFSFile(t, out, "original", 0644)
			failure := errors.New("external command failed")
			d := packageFilesFixture(t, nil, nil)
			b := newTestBundler(DI{FS: d.fs, Query: d.query, Ldd: ldd.NewResolver(ldd.ResolverOptions{Cwd: "/", FS: d.fs})})
			input := BundleInput{Out: rootfs.MustPath(out)}
			app := filepath.Join(dir, "app")
			writeRootFSFile(t, app, "app", 0755)
			b.scanner = stubScanner(func(path string) ([]*ElfDependency, error) {
				switch stage {
				case "file scan", "directory scan":
					return nil, failure
				case "broken library":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: rootfs.MustPath(path)}, BrokenLibs: []string{"libmissing.so"}}}, nil
				case "uninstalled dependency":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: rootfs.MustPath(path)}, Dependencies: []*ExeDependency{{Library: &ldd.Library{Path: rootfs.MustPath(filepath.Join(dir, "library.so"))}, Package: testPackage(t, "broken", "", "", "unpacked")}}}}, nil
				case "uninstalled target package":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: rootfs.MustPath(path)}, Package: testPackage(t, "broken", "", "", "unpacked")}}, nil
				}
				return nil, nil
			})
			var want string
			switch stage {
			case "file scan":
				input.Files = rootfs.MustPaths(app)
				want = failure.Error()
			case "directory scan":
				input.Files = rootfs.MustPaths("directory")
				want = failure.Error()
			case "packages":
				input.Packages = []string{"pkg"}
				d.query.GetFunc = func(string) (*dpkg.Package, error) { return nil, failure }
				want = failure.Error()
			case "broken library":
				input.Files = rootfs.MustPaths(app)
				want = "=> [libmissing.so]"
			case "uninstalled explicit package":
				input.Packages = []string{"broken"}
				d.query.GetFunc = func(string) (*dpkg.Package, error) { return testPackage(t, "broken", "", "", "config-files"), nil }
				want = "packages are not installed: broken"
			case "uninstalled dependency", "uninstalled target package":
				input.Files = rootfs.MustPaths(app)
				want = "packages are not installed: broken"
			case "baker":
				input.Out = rootfs.MustPath(filepath.Join(dir, "absent", "bundle.tar"))
				want = "no such file or directory"
			}
			b.BundleInput = input
			err := b.bundle()
			require.ErrorContains(t, err, want)
			if stage == "file scan" || stage == "directory scan" || stage == "packages" {
				assert.ErrorIs(t, err, failure)
			}
			data, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, "original", string(data))
			// A failed request must not affect a subsequent bundle.
			file := filepath.Join(dir, "valid")
			writeTestELF(t, file, elf.ET_EXEC, false)
			require.NoError(t, Bundle(b.DI, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(file)}))
			assert.Contains(t, readBundleTar(t, out), rootFSArchiveName(file))
		})
	}
}

func TestBundleRequestsUseFreshScanner(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "program")
	traces := 0
	l := teststub.NewResolver(t)
	l.ListObjectsFunc = func(rootfs.Path) ([]*ldd.Library, error) { traces++; return nil, nil }
	di := DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: l}
	for _, typ := range []elf.Type{elf.ET_REL, elf.ET_EXEC} {
		writeTestELF(t, file, typ, false)
		out := filepath.Join(dir, "bundle.tar")
		require.NoError(t, Bundle(di, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(file)}))
		entries := readBundleTar(t, out)
		assert.Contains(t, entries, rootFSArchiveName(file))
	}
	require.Equal(t, 1, traces, "a file changed from ET_REL to ET_EXEC must be scanned in the next request")
	other := filepath.Join(dir, "other")
	writeTestELF(t, other, elf.ET_EXEC, false)
	out := filepath.Join(dir, "other.tar")
	require.NoError(t, Bundle(di, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(other)}))
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(other))
	assert.NotContains(t, entries, rootFSArchiveName(file))
}

func TestBundlerCopyDirectory(t *testing.T) {
	for _, relative := range []bool{false, true} {
		name := "absolute"
		if relative {
			name = "relative"
		}
		t.Run(name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			t.Chdir(dir)
			tree := filepath.Join(dir, "tree")
			file := filepath.Join(tree, "nested", "config")
			empty := filepath.Join(tree, "empty")
			writeRootFSFile(t, file, "configuration", 0640)
			require.NoError(t, os.Mkdir(empty, 0750))
			require.NoError(t, os.Chmod(empty, 0750))
			source := tree
			if relative {
				source = "tree"
			}
			out := filepath.Join(dir, "bundle.tar")
			require.NoError(t, Bundle(DI{FS: os.DirFS("/")}, BundleInput{Out: rootfs.MustPath(out), Files: []rootfs.Path{rootfs.MustPath(dir).Resolve(source)}}))
			entries := readBundleTar(t, out)
			for _, path := range []string{tree, filepath.Dir(file), empty} {
				require.Contains(t, entries, rootFSArchiveName(path))
				assert.Equal(t, byte(tar.TypeDir), entries[rootFSArchiveName(path)].header.Typeflag)
			}
			assert.Equal(t, int64(0750), entries[rootFSArchiveName(empty)].header.Mode)
			require.Contains(t, entries, rootFSArchiveName(file))
			assert.Equal(t, "configuration", entries[rootFSArchiveName(file)].body)
			assert.Equal(t, int64(0640), entries[rootFSArchiveName(file)].header.Mode)
		})
	}
}

func TestBundlerCopySymlinks(t *testing.T) {
	dir := rootFSTempDir(t)
	tree := filepath.Join(dir, "tree")
	file := filepath.Join(dir, "outside", "file")
	empty := filepath.Join(dir, "outside", "empty")
	writeRootFSFile(t, file, "target content", 0644)
	require.NoError(t, os.Mkdir(empty, 0755))
	require.NoError(t, os.Mkdir(tree, 0755))
	links := map[string]string{
		"relative":  "../outside/file",
		"absolute":  file,
		"directory": "../outside/empty",
		"self":      ".",
	}
	for name, target := range links {
		require.NoError(t, os.Symlink(target, filepath.Join(tree, name)))
	}
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, Bundle(DI{FS: os.DirFS("/")}, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(tree)}))
	entries := readBundleTar(t, out)
	for name, target := range links {
		key := rootFSArchiveName(filepath.Join(tree, name))
		require.Contains(t, entries, key)
		assert.Equal(t, byte(tar.TypeSymlink), entries[key].header.Typeflag)
		assert.Equal(t, target, entries[key].header.Linkname)
	}
	// RootFS preserves each link and also copies its target, even outside the
	// explicitly copied tree. A self directory link must not be traversed.
	assert.Equal(t, "target content", entries[rootFSArchiveName(file)].body)
	require.Contains(t, entries, rootFSArchiveName(empty))
	assert.Equal(t, byte(tar.TypeDir), entries[rootFSArchiveName(empty)].header.Typeflag)
}

func TestBundlerCopyMergesAndDeduplicates(t *testing.T) {
	dir := rootFSTempDir(t)
	t.Chdir(dir)
	file := filepath.Join(dir, "tree", "program")
	copiedELF := filepath.Join(dir, "tree", "dynamic.so")
	writeTestELF(t, file, elf.ET_EXEC, false)
	writeTestELF(t, copiedELF, elf.ET_DYN, false, "not-installed.so")
	pkg := testPackage(t, "example:arm64", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {file}})
	out := filepath.Join(dir, "bundle.tar")
	l := teststub.NewResolver(t)
	l.ListObjectsFunc = func(path rootfs.Path) ([]*ldd.Library, error) {
		require.Equal(t, file, path.Abs(), "copied ELF files must not be scanned")
		return nil, nil
	}
	require.NoError(t, Bundle(DI{FS: d.fs, Query: d.query, Ldd: l}, BundleInput{
		Out: rootfs.MustPath(out), Packages: []string{pkg.NameWithArch()},
		Files:        rootfs.MustPath(dir).SplitList("tree:tree/program:tree:" + copiedELF),
		ExcludeScans: rootfs.MustPaths(copiedELF),
	}))
	entries := readBundleTar(t, out) // Also rejects duplicate tar entries.
	for _, path := range []string{file, copiedELF} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, entries, rootFSArchiveName(path))
		assert.Equal(t, string(data), entries[rootFSArchiveName(path)].body)
	}
	assert.Contains(t, entries, "var/lib/dpkg/status.d/example")
}

func TestBundlerCopyFailureAndReuse(t *testing.T) {
	for _, stage := range []string{"missing path", "dangling link"} {
		t.Run(stage, func(t *testing.T) {
			dir := rootFSTempDir(t)
			good := filepath.Join(dir, "good")
			bad := filepath.Join(dir, "missing")
			out := filepath.Join(dir, "bundle.tar")
			writeRootFSFile(t, good, "good content", 0644)
			writeRootFSFile(t, out, "previous archive", 0644)
			if stage == "dangling link" {
				bad = filepath.Join(dir, "tree", "broken")
				require.NoError(t, os.Mkdir(filepath.Dir(bad), 0755))
				require.NoError(t, os.Symlink("missing", bad))
				bad = filepath.Dir(bad)
			}
			di := DI{FS: os.DirFS("/")}
			err := Bundle(di, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(good, bad)})
			require.ErrorIs(t, err, os.ErrNotExist)
			original, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, "previous archive", string(original))
			other := filepath.Join(dir, "other")
			writeRootFSFile(t, other, "other content", 0644)
			require.NoError(t, Bundle(di, BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(other)}))
			entries := readBundleTar(t, out)
			assert.Equal(t, "other content", entries[rootFSArchiveName(other)].body)
			assert.NotContains(t, entries, rootFSArchiveName(good), "failed copy must not leak into the next bundle")
		})
	}
}

func TestBundleExclusionsAcrossCollectionRoutes(t *testing.T) {
	dir := rootFSTempDir(t)
	program := filepath.Join(dir, "app")
	library := filepath.Join(dir, "real", "libfoo.so")
	aliasDir := filepath.Join(dir, "alias")
	data := filepath.Join(dir, "real", "data")
	writeTestELF(t, program, elf.ET_EXEC, false)
	writeRootFSFile(t, library, "library", 0644)
	writeRootFSFile(t, data, "data", 0644)
	require.NoError(t, os.Symlink("real", aliasDir))
	pkg := testPackage(t, "library", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {library, data}})
	l := teststub.NewResolver(t)
	l.ListObjectsFunc = func(rootfs.Path) ([]*ldd.Library, error) {
		return []*ldd.Library{{Name: "libfoo.so", Path: rootfs.MustPath(library)}}, nil
	}
	out := filepath.Join(dir, "out.tar")
	require.NoError(t, Bundle(DI{FS: d.fs, Query: d.query, Ldd: l}, BundleInput{
		Out: rootfs.MustPath(out), Files: rootfs.MustPaths(program), Packages: []string{pkg.NameWithArch()},
		Excludes: rootfs.MustPaths(filepath.Join(aliasDir, "libfoo.so")),
	}))
	entries := readBundleTar(t, out)
	require.Contains(t, entries, rootFSArchiveName(program))
	require.Contains(t, entries, rootFSArchiveName(data))
	require.NotContains(t, entries, rootFSArchiveName(library), "exclude must apply to both dependencies and package contents")
}

func TestBundleScanExclusionThroughDirectoryAlias(t *testing.T) {
	dir := rootFSTempDir(t)
	tree := filepath.Join(dir, "real")
	tool := filepath.Join(tree, "nested", "tool")
	kept := filepath.Join(tree, "app")
	alias := filepath.Join(dir, "alias")
	writeTestELF(t, tool, elf.ET_EXEC, false, "missing.so")
	writeTestELF(t, kept, elf.ET_EXEC, false)
	require.NoError(t, os.Symlink("real", alias))
	var scanned []string
	l := teststub.NewResolver(t)
	l.ListObjectsFunc = func(p rootfs.Path) ([]*ldd.Library, error) { scanned = append(scanned, p.Abs()); return nil, nil }
	out := filepath.Join(dir, "out.tar")
	require.NoError(t, Bundle(DI{FS: os.DirFS("/"), Query: unownedFilesQuery(t), Ldd: l}, BundleInput{
		Out: rootfs.MustPath(out), Files: rootfs.MustPaths(alias, filepath.Join(alias, "nested", "tool")),
		ExcludeScans: rootfs.MustPaths(tool),
	}))
	require.Equal(t, []string{filepath.Join(alias, "app")}, scanned)
	entries := readBundleTar(t, out)
	require.Contains(t, entries, rootFSArchiveName(tool), "scan exclusion must still copy the ELF")
	require.Contains(t, entries, rootFSArchiveName(kept))
}

func TestBundlePropagatesDependencyExclusionResolutionError(t *testing.T) {
	dir := rootFSTempDir(t)
	app := filepath.Join(dir, "app")
	cycle := filepath.Join(dir, "cycle")
	out := filepath.Join(dir, "out.tar")
	writeRootFSFile(t, app, "application", 0755)
	writeRootFSFile(t, out, "previous archive", 0644)
	require.NoError(t, os.Symlink("cycle", cycle))
	b := newTestBundler(DI{FS: os.DirFS("/")})
	b.BundleInput = BundleInput{Out: rootfs.MustPath(out), Files: rootfs.MustPaths(app)}
	b.scanner = stubScanner(func(string) ([]*ElfDependency, error) {
		return []*ElfDependency{{ElfFile: &ElfFile{Path: rootfs.MustPath(app)}, Dependencies: []*ExeDependency{
			{Library: &ldd.Library{Path: rootfs.MustPath(cycle)}},
		}}}, nil
	})
	require.ErrorContains(t, b.bundle(), "circular symlink detected")
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "previous archive", string(data))
}

func TestBundleCopiesDirectoryAssetsAfterPackageCollection(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "directory"
		if symlink {
			name = "directory alias"
		}
		t.Run(name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			tree := filepath.Join(dir, "tree")
			config := filepath.Join(tree, "config")
			extension := filepath.Join(tree, "nested", "extension.so")
			empty := filepath.Join(tree, "empty")
			writeRootFSFile(t, config, "unlisted configuration", 0644)
			writeTestELF(t, extension, elf.ET_DYN, false, "missing.so")
			require.NoError(t, os.Mkdir(empty, 0755))
			input := tree
			if symlink {
				input = filepath.Join(dir, "alias")
				require.NoError(t, os.Symlink("tree", input))
			}
			pkg := testPackage(t, "app-data", "", "", dpkg.StatusInstalled)
			d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {tree}})
			out := filepath.Join(dir, "out.tar")
			require.NoError(t, Bundle(DI{FS: d.fs, Query: d.query}, BundleInput{
				Out: rootfs.MustPath(out), Files: rootfs.MustPaths(input), Packages: []string{pkg.NameWithArch()},
				ExcludeScans: rootfs.MustPaths(input),
			}))
			entries := readBundleTar(t, out)
			require.Equal(t, "unlisted configuration", entries[rootFSArchiveName(config)].body)
			require.Contains(t, entries, rootFSArchiveName(extension), "ELF excluded from scanning must still be copied")
			require.Contains(t, entries, rootFSArchiveName(empty))
			require.Contains(t, entries, "var/lib/dpkg/status.d/app-data")
			if symlink {
				require.Equal(t, byte(tar.TypeSymlink), entries[rootFSArchiveName(input)].header.Typeflag)
			}
		})
	}
}
