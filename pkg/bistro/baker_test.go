package bistro

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBakerCreatesArchive(t *testing.T) {
	dir := rootFSTempDir(t)
	owned, explicit := filepath.Join(dir, "owned"), filepath.Join(dir, "explicit")
	writeRootFSFile(t, owned, "package data", 0640)
	writeRootFSFile(t, explicit, "binary", 0755)
	pkg := testPackage(t, "example:arm64", "", "", dpkg.StatusInstalled)
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {owned}})
	out := filepath.Join(dir, "rootfs.tar")
	writeRootFSFile(t, out, "previous archive", 0644)
	err := NewBaker(DI{FS: d.fs, Query: d.query}).CreateRootFS(&BakeInput{Out: rootfs.MustPath(out), DpkgList: []*dpkg.Package{pkg}, Files: rootfs.MustPaths(explicit, owned, explicit)})
	require.NoError(t, err)
	entries := readBundleTar(t, out)
	assert.Equal(t, "package data", entries[rootFSArchiveName(owned)].body)
	assert.Equal(t, int64(0640), entries[rootFSArchiveName(owned)].header.Mode)
	assert.Equal(t, "binary", entries[rootFSArchiveName(explicit)].body)
	assert.Equal(t, int64(0755), entries[rootFSArchiveName(explicit)].header.Mode)
	metadata, err := dpkg.ParseStatus(entries["var/lib/dpkg/status.d/example"].body)
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	assert.Equal(t, pkg.String(), metadata[0].String())
	assert.Equal(t, "d41d8cd98f00b204e9800998ecf8427e  usr/share/example:arm64\n", entries["var/lib/dpkg/status.d/example.md5sums"].body)
	assert.NotContains(t, entries, "var/lib/dpkg/status.d/example:arm64")
	assert.NotContains(t, entries, "var/lib/dpkg/status.d/example:arm64.md5sums")
	leftovers, err := filepath.Glob(filepath.Join(dir, "rootfs[0-9]*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestBakerSkipsMissingPackageFiles(t *testing.T) {
	dir := rootFSTempDir(t)
	missing, owned := filepath.Join(dir, "missing"), filepath.Join(dir, "owned")
	writeRootFSFile(t, owned, "package data", 0644)
	pkg := testPackage(t, "example", "", "", "")
	d := packageFilesFixture(t, []*dpkg.Package{pkg}, map[string][]string{pkg.NameWithArch(): {missing, owned}})
	var stderr bytes.Buffer
	out := filepath.Join(dir, "archive.tar")
	err := NewBaker(DI{FS: d.fs, Query: d.query, Logger: NewLogger(io.Discard, &stderr)}).CreateRootFS(&BakeInput{
		Out: rootfs.MustPath(out), DpkgList: []*dpkg.Package{pkg},
	})
	require.NoError(t, err)
	entries := readBundleTar(t, out)
	assert.NotContains(t, entries, rootFSArchiveName(missing))
	assert.Equal(t, "package data", entries[rootFSArchiveName(owned)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/example")
	assert.Equal(t, "[ERROR] File does not exist: "+missing+"\n", stderr.String())
}

func TestBakerFailurePreservesOutputAndRemovesTemporaryFile(t *testing.T) {
	for _, stage := range []string{"md5sums", "file list", "package file", "explicit file", "rename"} {
		t.Run(stage, func(t *testing.T) {
			dir := rootFSTempDir(t)
			out := filepath.Join(dir, "archive.tar")
			if stage == "rename" {
				require.NoError(t, os.Mkdir(out, 0755))
			} else {
				writeRootFSFile(t, out, "original", 0644)
			}
			pkg := testPackage(t, "example", "", "", "")
			d := packageFilesFixture(t, []*dpkg.Package{pkg}, nil)
			if stage == "md5sums" || stage == "file list" {
				extension := ".md5sums"
				if stage == "file list" {
					extension = ".list"
				}
				path := filepath.Join(d.baseDir, "info", "example"+extension)
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0755))
			}
			if stage == "package file" {
				broken := filepath.Join(d.baseDir, "loop")
				require.NoError(t, os.Symlink("loop", broken))
				require.NoError(t, os.WriteFile(filepath.Join(d.baseDir, "info/example.list"), []byte(broken+"\n"), 0644))
			}
			input := &BakeInput{Out: rootfs.MustPath(out), DpkgList: []*dpkg.Package{pkg}}
			if stage == "explicit file" {
				input.Files = rootfs.MustPaths(filepath.Join(dir, "missing"))
			}
			err := NewBaker(DI{FS: d.fs, Query: d.query}).CreateRootFS(input)
			switch stage {
			case "md5sums", "file list":
				extension := ".md5sums"
				if stage == "file list" {
					extension = ".list"
				}
				var pathErr *os.PathError
				require.ErrorAs(t, err, &pathErr)
				assert.Equal(t, "read", pathErr.Op)
				assert.Equal(t, filepath.Join(d.baseDir, "info", "example"+extension), pathErr.Path)
				assert.ErrorIs(t, err, syscall.EISDIR)
			case "package file":
				require.ErrorContains(t, err, "circular symlink")
			case "explicit file":
				var pathErr *os.PathError
				require.ErrorAs(t, err, &pathErr)
				assert.Equal(t, "lstat", pathErr.Op)
				assert.Equal(t, rootfs.MustPath(filepath.Join(dir, "missing")).Path(), pathErr.Path)
				assert.ErrorIs(t, err, os.ErrNotExist)
			case "rename":
				// The errno for renaming a file over a directory differs by OS.
				var linkErr *os.LinkError
				require.ErrorAs(t, err, &linkErr)
				assert.Equal(t, "rename", linkErr.Op)
				assert.Equal(t, out, linkErr.New)
			}
			if stage != "rename" {
				data, e := os.ReadFile(out)
				require.NoError(t, e)
				assert.Equal(t, "original", string(data))
			}
			files, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, files, 1, "temporary archive must be removed on failure")
			assert.Equal(t, "archive.tar", files[0].Name())
		})
	}
}

func TestBakerMissingOutputDirectory(t *testing.T) {
	out := filepath.Join(t.TempDir(), "missing", "archive.tar")
	err := NewBaker(DI{FS: os.DirFS("/")}).CreateRootFS(&BakeInput{Out: rootfs.MustPath(out)})
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBakerRejectsConflictingMetadataNames(t *testing.T) {
	dir := rootFSTempDir(t)
	out := filepath.Join(dir, "archive.tar")
	writeRootFSFile(t, out, "original", 0644)
	err := NewBaker(DI{FS: os.DirFS("/")}).CreateRootFS(&BakeInput{
		Out: rootfs.MustPath(out),
		DpkgList: []*dpkg.Package{
			testPackage(t, "library:arm64", "", "arm64", ""),
			testPackage(t, "library:amd64", "", "amd64", ""),
		},
	})
	require.ErrorContains(t, err, "package metadata name conflict: library:arm64 and library:amd64 use library")
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "original", string(data))
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1, "no temporary archive should be created")
}

type failingMetadataRoot struct {
	rootfs.Archiver
	calls, failAt int
	err           error
}

func (r *failingMetadataRoot) Add(rootfs.Path, []byte, os.FileMode) error {
	r.calls++
	if r.calls == r.failAt {
		return r.err
	}
	return nil
}

func TestBakerMetadataWriteFailures(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			failure := errors.New("archive write failed")
			r := &failingMetadataRoot{failAt: failAt, err: failure}
			pkg := testPackage(t, "example", "", "", "")
			d := packageFilesFixture(t, []*dpkg.Package{pkg}, nil)
			b := NewBaker(DI{FS: d.fs}).(*baker)
			require.ErrorIs(t, b.writePackageMetadata(r, pkg, rootfs.PathSet{}), failure)
			assert.Equal(t, failAt, r.calls)
		})
	}
}

func TestBakerExcludesArchivesInsideSourceDirectory(t *testing.T) {
	for _, mode := range []string{"relative output", "absolute output", "symlink output directory"} {
		t.Run(mode, func(t *testing.T) {
			dir := rootFSTempDir(t)
			source := filepath.Join(dir, "source")
			config := filepath.Join(source, "config")
			asset := filepath.Join(source, "nested", "asset")
			writeRootFSFile(t, asset, "asset data", 0644)
			t.Chdir(source)
			out := filepath.Join(source, "bundle.tar")
			switch mode {
			case "relative output":
				out = "bundle.tar"
			case "symlink output directory":
				alias := filepath.Join(dir, "alias")
				require.NoError(t, os.Symlink("source", alias))
				out = filepath.Join(alias, "bundle.tar")
			}
			baker := NewBaker(DI{FS: os.DirFS("/")})
			for _, content := range []string{"first version", "second version"} {
				// The second request must also exclude the archive from the first request.
				writeRootFSFile(t, config, content, 0644)
				require.NoError(t, baker.CreateRootFS(&BakeInput{Out: rootfs.MustPath(source).Resolve(out), Files: rootfs.MustPaths(source)}))
				entries := readBundleTar(t, out)
				require.Equal(t, content, entries[rootFSArchiveName(config)].body)
				require.Equal(t, "asset data", entries[rootFSArchiveName(asset)].body)
				var descendants []string
				prefix := rootFSArchiveName(source) + "/"
				for name := range entries {
					if relative, ok := strings.CutPrefix(name, prefix); ok {
						descendants = append(descendants, relative)
					}
				}
				require.ElementsMatch(t, []string{"config", "nested", "nested/asset"}, descendants,
					"neither the output archive nor the temporary archive may be bundled")
				files, err := os.ReadDir(source)
				require.NoError(t, err)
				var names []string
				for _, file := range files {
					names = append(names, file.Name())
				}
				require.ElementsMatch(t, []string{"config", "nested", "bundle.tar"}, names,
					"no temporary archive may remain after success")
			}
		})
	}
}
