package bistro

import (
	"archive/tar"
	"debug/elf"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundlerMergesInputs(t *testing.T) {
	dir := rootFSTempDir(t)
	file, scan := filepath.Join(dir, "program"), filepath.Join(dir, "extension.so")
	owned := filepath.Join(dir, "library.so")
	writeRootFSFile(t, file, "program", 0755)
	writeRootFSFile(t, scan, "scan target", 0644)
	writeRootFSFile(t, owned, "shared library", 0644)
	pkg := &Package{Name: "library", Package: "library:arm64", Status: PackageStatusInstalled}
	other := &Package{Name: "other", Package: "other:arm64", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg, other}, map[string][]string{pkg.Package: {owned}})
	queries := 0
	d.listPackages = func(names ...string) ([]*Package, error) {
		assert.Equal(t, []string{pkg.Package, other.Package}, names)
		return []*Package{pkg, other}, nil
	}
	status := d.status
	d.status = func(name string) ([]byte, error) { queries++; return status(name) }
	b := NewBundler(DI{Dpkg: d})
	var scanned []string
	b.scanner = stubScanner(func(path string) ([]*ElfDependency, error) {
		scanned = append(scanned, path)
		return []*ElfDependency{{ElfFile: &ElfFile{Path: path}, Dependencies: []*ExeDependency{{Library: &Library{Path: "/lib/unowned.so"}}, {Package: pkg}, {Package: &Package{Name: pkg.Name, Package: pkg.Package, Status: pkg.Status}}}}}, nil
	})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, b.Bundle(BundleInput{Out: out, Files: []string{file, file}, Scans: []string{scan}, Packages: []string{pkg.Package, other.Package}}))
	assert.Equal(t, []string{file, file, scan}, scanned)
	assert.Equal(t, 2, queries, "one metadata entry per package")
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(file))
	assert.Contains(t, entries, rootFSArchiveName(owned))
	assert.NotContains(t, entries, rootFSArchiveName(scan))
	assert.Contains(t, entries, "var/lib/dpkg/status.d/library")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/other")
}

func TestBundlerPackageOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "font.ttf")
	writeRootFSFile(t, file, "font data", 0644)
	pkg := &Package{Name: "fonts", Package: "fonts", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg}, map[string][]string{pkg.Package: {file}})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(DI{Dpkg: d}).Bundle(BundleInput{Out: out, Packages: []string{"fonts"}}))
	assert.Equal(t, "font data", readBundleTar(t, out)[rootFSArchiveName(file)].body)
}

func TestBundlerScanOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "static")
	writeTestELF(t, file, elf.ET_EXEC, false)
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(DI{}).Bundle(BundleInput{Out: out, Scans: []string{file}}))
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
	pkg := &Package{Name: "libfoo", Package: "libfoo:arm64", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg}, map[string][]string{pkg.Package: {library}})
	searches, queries, traces := 0, 0, 0
	d.search = func(path string) ([]*OwnedFile, error) {
		searches++
		assert.Equal(t, library, path)
		return []*OwnedFile{{Package: pkg.Package, File: path}}, nil
	}
	d.listPackages = func(names ...string) ([]*Package, error) {
		queries++
		assert.Equal(t, []string{pkg.Package}, names)
		return []*Package{pkg}, nil
	}
	l := stubLdd(func(path string) (*LinkInfo, error) {
		traces++
		assert.Contains(t, []string{program, extension}, path)
		return &LinkInfo{Libs: []*Library{{Name: "libfoo.so", Path: library}}}, nil
	})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(DI{Dpkg: d, Ldd: l}).Bundle(BundleInput{
		Out: out, Files: []string{program}, Scans: []string{extension, program},
	}))
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(program))
	assert.NotContains(t, entries, rootFSArchiveName(extension))
	assert.Equal(t, "runtime library", entries[rootFSArchiveName(library)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/libfoo")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/libfoo.md5sums")
	assert.Equal(t, 2, traces)
	assert.Equal(t, 1, searches)
	assert.Equal(t, 1, queries)
}

func TestBundlerFailures(t *testing.T) {
	for _, stage := range []string{"file scan", "directory scan", "packages", "broken library", "uninstalled explicit package", "uninstalled dependency", "baker"} {
		t.Run(stage, func(t *testing.T) {
			dir := rootFSTempDir(t)
			out := filepath.Join(dir, "bundle.tar")
			writeRootFSFile(t, out, "original", 0644)
			failure := errors.New("external command failed")
			d := packageFilesStub(t, nil, nil)
			b := NewBundler(DI{Dpkg: d})
			input := BundleInput{Out: out}
			b.scanner = stubScanner(func(path string) ([]*ElfDependency, error) {
				switch stage {
				case "file scan", "directory scan":
					return nil, failure
				case "broken library":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: path}, BrokenLibs: []string{"libmissing.so"}}}, nil
				case "uninstalled dependency":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: path}, Dependencies: []*ExeDependency{{Package: &Package{Name: "broken", Package: "broken", Status: "unpacked"}}}}}, nil
				}
				return nil, nil
			})
			var want string
			switch stage {
			case "file scan":
				input.Files = []string{"app"}
				want = failure.Error()
			case "directory scan":
				input.Scans = []string{"directory"}
				want = failure.Error()
			case "packages":
				input.Packages = []string{"pkg"}
				d.listPackages = func(...string) ([]*Package, error) { return nil, failure }
				b.Dpkg = d
				want = failure.Error()
			case "broken library":
				input.Files = []string{"app"}
				want = "app => [libmissing.so]"
			case "uninstalled explicit package":
				input.Packages = []string{"broken"}
				d.listPackages = func(...string) ([]*Package, error) {
					return []*Package{{Package: "broken", Status: "config-files"}}, nil
				}
				b.Dpkg = d
				want = "packages are not installed: broken"
			case "uninstalled dependency":
				input.Scans = []string{"app"}
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
			require.NoError(t, b.Bundle(BundleInput{Out: out, Files: []string{file}}))
			assert.Contains(t, readBundleTar(t, out), rootFSArchiveName(file))
		})
	}
}

func TestBundlerReuseRefreshesScanner(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "program")
	b := NewBundler(DI{})
	for i, typ := range []elf.Type{elf.ET_REL, elf.ET_EXEC} {
		writeTestELF(t, file, typ, false)
		out := filepath.Join(dir, "bundle.tar")
		require.NoError(t, b.Bundle(BundleInput{Out: out, Files: []string{file}}))
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
	require.NoError(t, b.Bundle(BundleInput{Out: out, Files: []string{other}}))
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
			require.NoError(t, NewBundler(DI{}).Bundle(BundleInput{Out: out, Copies: []string{source}}))
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
	require.NoError(t, NewBundler(DI{}).Bundle(BundleInput{Out: out, Copies: []string{tree}}))
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
	pkg := &Package{Name: "example", Package: "example:arm64", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg}, map[string][]string{pkg.Package: {file}})
	out := filepath.Join(dir, "bundle.tar")
	// A nil Ldd makes an unexpected dependency scan of a copied ELF fail.
	require.NoError(t, NewBundler(DI{Dpkg: d}).Bundle(BundleInput{
		Out: out, Files: []string{file}, Scans: []string{file}, Packages: []string{pkg.Package},
		Copies: []string{"tree", "tree/program", "tree", copiedELF},
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
			b := NewBundler(DI{})
			err := b.Bundle(BundleInput{Out: out, Copies: []string{good, bad}})
			require.ErrorIs(t, err, os.ErrNotExist)
			original, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, "previous archive", string(original))
			other := filepath.Join(dir, "other")
			writeRootFSFile(t, other, "other content", 0644)
			require.NoError(t, b.Bundle(BundleInput{Out: out, Copies: []string{other}}))
			entries := readBundleTar(t, out)
			assert.Equal(t, "other content", entries[rootFSArchiveName(other)].body)
			assert.NotContains(t, entries, rootFSArchiveName(good), "failed copy must not leak into the next bundle")
		})
	}
}
