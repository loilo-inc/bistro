package bistro

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBakerCreatesArchive(t *testing.T) {
	dir := rootFSTempDir(t)
	owned, explicit := filepath.Join(dir, "owned"), filepath.Join(dir, "explicit")
	writeRootFSFile(t, owned, "package data", 0640)
	writeRootFSFile(t, explicit, "binary", 0755)
	pkg := &Package{Name: "example", Package: "example:arm64", Status: PackageStatusInstalled}
	d := packageFilesStub(t, []*Package{pkg}, map[string][]string{pkg.Package: {owned}})
	out := filepath.Join(dir, "rootfs.tar")
	writeRootFSFile(t, out, "previous archive", 0644)
	err := NewBaker(DI{Dpkg: d}).CreateRootFS(&BakeInput{Out: out, DpkgList: []*Package{pkg}, Files: []string{explicit, owned, explicit}})
	require.NoError(t, err)
	entries := readBundleTar(t, out)
	assert.Equal(t, "package data", entries[rootFSArchiveName(owned)].body)
	assert.Equal(t, int64(0640), entries[rootFSArchiveName(owned)].header.Mode)
	assert.Equal(t, "binary", entries[rootFSArchiveName(explicit)].body)
	assert.Equal(t, int64(0755), entries[rootFSArchiveName(explicit)].header.Mode)
	assert.Equal(t, "Package: example\nStatus: install ok installed\n", entries["var/lib/dpkg/status.d/example"].body)
	assert.Equal(t, "checksum example:arm64\n", entries["var/lib/dpkg/status.d/example.md5sums"].body)
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
	pkg := &Package{Name: "example", Package: "example"}
	d := packageFilesStub(t, []*Package{pkg}, map[string][]string{pkg.Package: {missing, owned}})
	var stderr bytes.Buffer
	out := filepath.Join(dir, "archive.tar")
	err := NewBaker(DI{Dpkg: d, Logger: NewLogger(io.Discard, &stderr)}).CreateRootFS(&BakeInput{
		Out: out, DpkgList: []*Package{pkg},
	})
	require.NoError(t, err)
	entries := readBundleTar(t, out)
	assert.NotContains(t, entries, rootFSArchiveName(missing))
	assert.Equal(t, "package data", entries[rootFSArchiveName(owned)].body)
	assert.Contains(t, entries, "var/lib/dpkg/status.d/example")
	assert.Equal(t, "[ERROR] File does not exist: "+missing+"\n", stderr.String())
}

func TestBakerFailurePreservesOutputAndRemovesTemporaryFile(t *testing.T) {
	for _, stage := range []string{"status", "md5sums", "file list", "package file", "explicit file", "rename"} {
		t.Run(stage, func(t *testing.T) {
			dir := rootFSTempDir(t)
			out := filepath.Join(dir, "archive.tar")
			if stage == "rename" {
				require.NoError(t, os.Mkdir(out, 0755))
			} else {
				writeRootFSFile(t, out, "original", 0644)
			}
			failure := errors.New("metadata unavailable")
			pkg := &Package{Name: "example", Package: "example"}
			d := packageFilesStub(t, []*Package{pkg}, nil)
			if stage == "status" {
				d.status = func(string) ([]byte, error) { return nil, failure }
			}
			if stage == "md5sums" {
				d.md5sums = func(string) ([]byte, error) { return nil, failure }
			}
			if stage == "file list" {
				d.listFiles = func(string) ([]string, error) { return nil, failure }
			}
			if stage == "package file" {
				d.listFiles = func(string) ([]string, error) { return []string{"relative"}, nil }
			}
			input := &BakeInput{Out: out, DpkgList: []*Package{pkg}}
			if stage == "explicit file" {
				input.Files = []string{filepath.Join(dir, "missing")}
			}
			err := NewBaker(DI{Dpkg: d}).CreateRootFS(input)
			require.Error(t, err)
			if stage == "status" || stage == "md5sums" || stage == "file list" {
				assert.ErrorIs(t, err, failure)
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
	err := NewBaker(DI{}).CreateRootFS(&BakeInput{Out: out})
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBakerRejectsConflictingMetadataNames(t *testing.T) {
	dir := rootFSTempDir(t)
	out := filepath.Join(dir, "archive.tar")
	writeRootFSFile(t, out, "original", 0644)
	err := NewBaker(DI{}).CreateRootFS(&BakeInput{
		Out: out,
		DpkgList: []*Package{
			{Name: "library", Package: "library:arm64", Architecture: "arm64"},
			{Name: "library", Package: "library:amd64", Architecture: "amd64"},
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
	RootFS
	calls, failAt int
	err           error
}

func (r *failingMetadataRoot) Add(string, []byte, os.FileMode) error {
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
			b := NewBaker(DI{Dpkg: packageFilesStub(t, nil, nil)}).(*baker)
			require.ErrorIs(t, b.writePackageMetadata(r, &Package{Name: "example", Package: "example"}), failure)
			assert.Equal(t, failAt, r.calls)
		})
	}
}
