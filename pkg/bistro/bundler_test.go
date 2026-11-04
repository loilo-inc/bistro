package bistro

import (
	"archive/tar"
	"debug/elf"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
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
	d := packageFilesStub(t, []*dpkg.Package{pkg, other}, map[string][]string{pkg.NameWithArch(): {owned}})
	var requested []string
	get := d.get
	d.get = func(name string) (*dpkg.Package, error) {
		requested = append(requested, name)
		return get(name)
	}
	b := NewBundler(DI{FS: testFS(d), Query: d})
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
	require.NoError(t, b.Bundle(BundleInput{Out: out, Files: rootfs.MustPaths(file, file), Scans: rootfs.MustPaths(scan), Packages: []string{pkg.NameWithArch(), other.NameWithArch()}}))
	assert.Equal(t, []string{file, file, scan}, scanned)
	assert.Equal(t, []string{pkg.NameWithArch(), other.NameWithArch()}, requested)
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(file))
	assert.Contains(t, entries, rootFSArchiveName(owned))
	assert.Equal(t, "unowned library", entries[rootFSArchiveName(unowned)].body)
	assert.NotContains(t, entries, rootFSArchiveName(scan))
	assert.Contains(t, entries, "var/lib/dpkg/status.d/library")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/other")
}

func TestBundlerPackageOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "font.ttf")
	writeRootFSFile(t, file, "font data", 0644)
	pkg := testPackage(t, "fonts", "", "", dpkg.StatusInstalled)
	d := packageFilesStub(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {file}})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(DI{FS: testFS(d), Query: d}).Bundle(BundleInput{Out: out, Packages: []string{"fonts"}}))
	assert.Equal(t, "font data", readBundleTar(t, out)[rootFSArchiveName(file)].body)
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
	q := testQuery(baseDir)
	require.NoError(t, q.Load())
	b := NewBundler(DI{FS: testFS(q), Query: q})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, b.Bundle(BundleInput{Out: out, Packages: []string{"fonts"}}))
	entries := readBundleTar(t, out)
	assert.Equal(t, "font data", entries[rootFSArchiveName(file)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/fonts")
	assert.NotContains(t, entries, "var/lib/dpkg/status.d/library")

	// Selecting both architectures still conflicts in distroless metadata.
	err := b.Bundle(BundleInput{Out: out, Packages: []string{"library:amd64", "library:arm64"}})
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
				d := packageFilesStub(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {program, data}})
				traces := 0
				l := stubLdd(func(path string) ([]*ldd.Library, error) {
					traces++
					assert.Equal(t, target, path)
					return nil, nil
				})
				out := filepath.Join(dir, "bundle.tar")
				require.NoError(t, NewBundler(DI{FS: testFS(d), Query: d, Ldd: l}).Bundle(BundleInput{Out: out, Files: rootfs.MustPaths(target)}))
				entries := readBundleTar(t, out)
				assert.Contains(t, entries, rootFSArchiveName(program))
				assert.Equal(t, "application data", entries[rootFSArchiveName(data)].body)
				assert.Contains(t, entries, "var/lib/dpkg/status.d/app")
				assert.Contains(t, entries, "var/lib/dpkg/status.d/app.md5sums")
				if symlink {
					assert.Equal(t, byte(tar.TypeSymlink), entries[rootFSArchiveName(target)].header.Typeflag)
				}
				if dynamic {
					assert.Equal(t, 1, traces)
				} else {
					assert.Zero(t, traces)
				}
			})
		}
	}
}

func TestBundlerScanOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "static")
	writeTestELF(t, file, elf.ET_EXEC, false)
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(DI{FS: testFS(nil), Query: unownedFilesQuery()}).Bundle(BundleInput{Out: out, Scans: rootfs.MustPaths(file)}))
	assert.Empty(t, readBundleTar(t, out), "a scan contributes dependencies, not its target")
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
	d := packageFilesStub(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {library}})
	lookups, traces := 0, 0
	d.lookup = func(path string) (*dpkg.Package, error) {
		lookups++
		if path != library {
			assert.Contains(t, []string{program, extension}, path)
			return nil, nil
		}
		return pkg, nil
	}
	l := stubLdd(func(path string) ([]*ldd.Library, error) {
		traces++
		assert.Contains(t, []string{program, extension}, path)
		return []*ldd.Library{{Name: "libfoo.so", Path: rootfs.MustPath(library)}}, nil
	})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(DI{FS: testFS(d), Query: d, Ldd: l}).Bundle(BundleInput{
		Out: out, Files: rootfs.MustPaths(program), Scans: rootfs.MustPaths(extension, program),
	}))
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(program))
	assert.NotContains(t, entries, rootFSArchiveName(extension))
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
			d := packageFilesStub(t, nil, nil)
			b := NewBundler(DI{FS: testFS(d), Query: d})
			input := BundleInput{Out: out}
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
				input.Files = rootfs.MustPaths("app")
				want = failure.Error()
			case "directory scan":
				input.Scans = rootfs.MustPaths("directory")
				want = failure.Error()
			case "packages":
				input.Packages = []string{"pkg"}
				d.get = func(string) (*dpkg.Package, error) { return nil, failure }
				b.Query = d
				want = failure.Error()
			case "broken library":
				input.Files = rootfs.MustPaths("app")
				want = "app => [libmissing.so]"
			case "uninstalled explicit package":
				input.Packages = []string{"broken"}
				d.get = func(string) (*dpkg.Package, error) { return testPackage(t, "broken", "", "", "config-files"), nil }
				b.Query = d
				want = "packages are not installed: broken"
			case "uninstalled dependency", "uninstalled target package":
				input.Scans = rootfs.MustPaths("app")
				want = "packages are not installed: broken"
			case "baker":
				input.Out = filepath.Join(dir, "absent", "bundle.tar")
				want = "no such file or directory"
			}
			err := b.Bundle(input)
			require.ErrorContains(t, err, want)
			if stage == "file scan" || stage == "directory scan" || stage == "packages" {
				assert.ErrorIs(t, err, failure)
			}
			data, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, "original", string(data))
			// A failed request must not poison a later request on the same instance.
			file := filepath.Join(dir, "valid")
			writeTestELF(t, file, elf.ET_EXEC, false)
			require.NoError(t, b.Bundle(BundleInput{Out: out, Files: rootfs.MustPaths(file)}))
			assert.Contains(t, readBundleTar(t, out), rootFSArchiveName(file))
		})
	}
}

func TestBundlerReuseRefreshesScanner(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "program")
	b := NewBundler(DI{FS: testFS(nil), Query: unownedFilesQuery()})
	for i, typ := range []elf.Type{elf.ET_REL, elf.ET_EXEC} {
		writeTestELF(t, file, typ, false)
		out := filepath.Join(dir, "bundle.tar")
		require.NoError(t, b.Bundle(BundleInput{Out: out, Files: rootfs.MustPaths(file)}))
		entries := readBundleTar(t, out)
		if i == 0 {
			assert.NotContains(t, entries, rootFSArchiveName(file))
		} else {
			assert.Contains(t, entries, rootFSArchiveName(file))
		}
	}
	other := filepath.Join(dir, "other")
	writeTestELF(t, other, elf.ET_EXEC, false)
	out := filepath.Join(dir, "other.tar")
	require.NoError(t, b.Bundle(BundleInput{Out: out, Files: rootfs.MustPaths(other)}))
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
			require.NoError(t, NewBundler(DI{FS: testFS(nil)}).Bundle(BundleInput{Out: out, Copies: []rootfs.Path{rootfs.MustPath(dir).Resolve(source)}}))
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
	require.NoError(t, NewBundler(DI{FS: testFS(nil)}).Bundle(BundleInput{Out: out, Copies: rootfs.MustPaths(tree)}))
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
	d := packageFilesStub(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {file}})
	out := filepath.Join(dir, "bundle.tar")
	// A nil Ldd makes an unexpected dependency scan of a copied ELF fail.
	require.NoError(t, NewBundler(DI{FS: testFS(d), Query: d}).Bundle(BundleInput{
		Out: out, Files: rootfs.MustPaths(file), Scans: rootfs.MustPaths(file), Packages: []string{pkg.NameWithArch()},
		Copies: rootfs.MustPath(dir).SplitList("tree:tree/program:tree:" + copiedELF),
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
			b := NewBundler(DI{FS: testFS(nil)})
			err := b.Bundle(BundleInput{Out: out, Copies: rootfs.MustPaths(good, bad)})
			require.ErrorIs(t, err, os.ErrNotExist)
			original, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, "previous archive", string(original))
			other := filepath.Join(dir, "other")
			writeRootFSFile(t, other, "other content", 0644)
			require.NoError(t, b.Bundle(BundleInput{Out: out, Copies: rootfs.MustPaths(other)}))
			entries := readBundleTar(t, out)
			assert.Equal(t, "other content", entries[rootFSArchiveName(other)].body)
			assert.NotContains(t, entries, rootFSArchiveName(good), "failed copy must not leak into the next bundle")
		})
	}
}
