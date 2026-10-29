package bistro

import (
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
	pkg := &Package{Package: "library:arm64", Status: PackageStatusInstalled}
	otherArch := &Package{Package: "library:amd64", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg, otherArch}, map[string][]string{pkg.Package: {owned}})
	queries := 0
	d.listPackages = func(names ...string) ([]*Package, error) {
		assert.Equal(t, []string{pkg.Package, otherArch.Package}, names)
		return []*Package{pkg, otherArch}, nil
	}
	status := d.status
	d.status = func(name string) ([]byte, error) { queries++; return status(name) }
	b := NewBundler(d, nil)
	var scanned []string
	b.scanner = stubScanner(func(path string) ([]*ElfDependency, error) {
		scanned = append(scanned, path)
		return []*ElfDependency{{ElfFile: &ElfFile{Path: path}, Dependencies: []*ExeDependency{{Package: pkg}, {Package: &Package{Package: pkg.Package, Status: pkg.Status}}}}}, nil
	})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, b.Bundle(BundleInput{Out: out, Files: []string{file, file}, Scans: []string{scan}, Packages: []string{pkg.Package, otherArch.Package}}))
	assert.Equal(t, []string{file, file, scan}, scanned)
	assert.Equal(t, 2, queries, "one metadata entry per package including architecture")
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(file))
	assert.Contains(t, entries, rootFSArchiveName(owned))
	assert.NotContains(t, entries, rootFSArchiveName(scan))
	assert.Contains(t, entries, "var/lib/dpkg/status.d/library:arm64")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/library:amd64")
}

func TestBundlerPackageOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "font.ttf")
	writeRootFSFile(t, file, "font data", 0644)
	pkg := &Package{Package: "fonts", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg}, map[string][]string{pkg.Package: {file}})
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(d, nil).Bundle(BundleInput{Out: out, Packages: []string{"fonts"}}))
	assert.Equal(t, "font data", readBundleTar(t, out)[rootFSArchiveName(file)].body)
}

func TestBundlerScanOnly(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "static")
	writeTestELF(t, file, elf.ET_EXEC, false)
	out := filepath.Join(dir, "bundle.tar")
	require.NoError(t, NewBundler(nil, nil).Bundle(BundleInput{Out: out, Scans: []string{file}}))
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
	pkg := &Package{Package: "libfoo:arm64", Status: PackageStatusInstalled}
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
	require.NoError(t, NewBundler(d, l).Bundle(BundleInput{
		Out: out, Files: []string{program}, Scans: []string{extension, program},
	}))
	entries := readBundleTar(t, out)
	assert.Contains(t, entries, rootFSArchiveName(program))
	assert.NotContains(t, entries, rootFSArchiveName(extension))
	assert.Equal(t, "runtime library", entries[rootFSArchiveName(library)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/libfoo:arm64")
	assert.Contains(t, entries, "var/lib/dpkg/status.d/libfoo:arm64.md5sums")
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
			b := NewBundler(d, nil)
			input := BundleInput{Out: out}
			b.scanner = stubScanner(func(path string) ([]*ElfDependency, error) {
				switch stage {
				case "file scan", "directory scan":
					return nil, failure
				case "broken library":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: path}, BrokenLibs: []string{"libmissing.so"}}}, nil
				case "uninstalled dependency":
					return []*ElfDependency{{ElfFile: &ElfFile{Path: path}, Dependencies: []*ExeDependency{{Package: &Package{Package: "broken", Status: "unpacked"}}}}}, nil
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
				b.dpkg = d
				want = failure.Error()
			case "broken library":
				input.Files = []string{"app"}
				want = "app => [libmissing.so]"
			case "uninstalled explicit package":
				input.Packages = []string{"broken"}
				d.listPackages = func(...string) ([]*Package, error) {
					return []*Package{{Package: "broken", Status: "config-files"}}, nil
				}
				b.dpkg = d
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
	b := NewBundler(nil, nil)
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
