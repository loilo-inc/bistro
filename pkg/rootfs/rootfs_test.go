package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rootFSEntry struct {
	header tar.Header
	body   string
}

func rootFSTempDir(t *testing.T) string {
	t.Helper()
	// macOS may place TempDir beneath a symlink such as /var.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func writeRootFSFile(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0755))
	require.NoError(t, os.WriteFile(name, []byte(content), mode))
	require.NoError(t, os.Chmod(name, mode))
}

func readRootFSTar(t *testing.T, data []byte) map[string]rootFSEntry {
	t.Helper()
	entries := make(map[string]rootFSEntry)
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.False(t, path.IsAbs(header.Name), "absolute archive path: %q", header.Name)
		require.NotEqual(t, "..", header.Name)
		require.False(t, strings.HasPrefix(header.Name, "../"), "invalid archive path: %q", header.Name)
		require.NotContains(t, entries, header.Name, "duplicate archive entry")
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		entries[header.Name] = rootFSEntry{header: *header, body: string(body)}
	}
	return entries
}

func rootFSArchiveName(name string) string {
	return strings.TrimPrefix(filepath.ToSlash(name), "/")
}

func TestRootFSAdd(t *testing.T) {
	for _, tt := range []struct {
		name    string
		path    string
		want    string
		content []byte
		mode    os.FileMode
	}{
		{"absolute path", "/etc/app/config", "etc/app/config", []byte("enabled=true\n"), 0640},
		{"relative path", "usr/bin/tool", "usr/bin/tool", []byte("#!/bin/sh\nexit 0\n"), 0755},
		{"empty file", "/var/lib/app/empty", "var/lib/app/empty", nil, 0600},
		{"binary content", "/opt/app/data", "opt/app/data", bytes.Repeat([]byte{0x00, 0xff, 0x80}, 400), 0644},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			root := New(&out)
			require.NoError(t, root.Add(tt.path, tt.content, tt.mode))
			// A following entry verifies that the content size and tar padding
			// do not corrupt the next file.
			require.NoError(t, root.Add("next", []byte("next file"), 0644))
			require.NoError(t, root.Close())
			entries := readRootFSTar(t, out.Bytes())
			require.Contains(t, entries, tt.want)
			got := entries[tt.want]
			assert.Equal(t, byte(tar.TypeReg), got.header.Typeflag)
			assert.Equal(t, int64(tt.mode), got.header.Mode)
			assert.Equal(t, int64(len(tt.content)), got.header.Size)
			assert.Equal(t, string(tt.content), got.body)
			assert.Len(t, entries, 2)
			require.Contains(t, entries, "next")
			assert.Equal(t, "next file", entries["next"].body)
		})
	}
}

type rootFSContentFailWriter struct {
	content []byte
	err     error
}

func (w rootFSContentFailWriter) Write(p []byte) (int, error) {
	if bytes.Equal(p, w.content) {
		return 0, w.err
	}
	return len(p), nil
}

func TestRootFSAddWriteErrors(t *testing.T) {
	want := errors.New("write failed")
	content := []byte("file content")
	for _, tt := range []struct {
		name   string
		writer io.Writer
	}{
		{"header", &rootFSFailWriter{err: want}},
		{"content", rootFSContentFailWriter{content: content, err: want}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := New(tt.writer)
			assert.ErrorIs(t, root.Add("file", content, 0644), want)
		})
	}
}

func TestRootFSFilesAndDirectories(t *testing.T) {
	dir := rootFSTempDir(t)
	first := filepath.Join(dir, "bin", "tool")
	second := filepath.Join(dir, "share", "tool")
	writeRootFSFile(t, first, "executable\n", 0751)
	writeRootFSFile(t, second, "data\n", 0640)
	require.NoError(t, os.Chmod(filepath.Dir(first), 0750))
	var out bytes.Buffer
	root := New(&out)
	for _, name := range []string{first, second, first, filepath.Dir(first)} {
		require.NoError(t, root.Copy(name))
	}
	require.NoError(t, root.Close())
	entries := readRootFSTar(t, out.Bytes())
	for _, want := range []struct {
		name string
		body string
		mode int64
	}{
		{first, "executable\n", 0751},
		{second, "data\n", 0640},
	} {
		require.Contains(t, entries, rootFSArchiveName(want.name))
		got := entries[rootFSArchiveName(want.name)]
		assert.Equal(t, byte(tar.TypeReg), got.header.Typeflag, want.name)
		assert.Equal(t, want.body, got.body, want.name)
		assert.Equal(t, want.mode, got.header.Mode, want.name)
	}
	require.Contains(t, entries, rootFSArchiveName(filepath.Dir(first)))
	parent := entries[rootFSArchiveName(filepath.Dir(first))]
	assert.Equal(t, byte(tar.TypeDir), parent.header.Typeflag)
	assert.Equal(t, int64(0750), parent.header.Mode)
	require.Contains(t, entries, ".")
	assert.Equal(t, byte(tar.TypeDir), entries["."].header.Typeflag)
}

func TestRootFSSymlinks(t *testing.T) {
	for _, tt := range []struct {
		name      string
		directory bool
		absolute  bool
		chain     bool
	}{
		{name: "relative directory", directory: true},
		{name: "absolute directory", directory: true, absolute: true},
		{name: "relative file"},
		{name: "absolute file", absolute: true},
		{name: "directory link chain", directory: true, chain: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			file := filepath.Join(dir, "usr", "lib", "nested", "foo")
			writeRootFSFile(t, file, "library contents", 0644)
			target := file
			remaining := ""
			if tt.directory {
				target = filepath.Join(dir, "usr", "lib")
				remaining = filepath.Join("nested", "foo")
			}
			link := target
			if !tt.absolute {
				var err error
				link, err = filepath.Rel(dir, target)
				require.NoError(t, err)
			}
			links := map[string]string{filepath.Join(dir, "alias"): link}
			if tt.chain {
				links[filepath.Join(dir, "intermediate")] = link
				links[filepath.Join(dir, "alias")] = "intermediate"
			}
			for name, target := range links {
				require.NoError(t, os.Symlink(target, name))
			}
			requested := filepath.Join(dir, "alias", remaining)
			var out bytes.Buffer
			root := New(&out)
			for _, name := range []string{requested, requested, file} {
				require.NoError(t, root.Copy(name))
			}
			require.NoError(t, root.Close())
			entries := readRootFSTar(t, out.Bytes())
			for name, target := range links {
				require.Contains(t, entries, rootFSArchiveName(name))
				got := entries[rootFSArchiveName(name)]
				assert.Equal(t, byte(tar.TypeSymlink), got.header.Typeflag, name)
				assert.Equal(t, target, got.header.Linkname, name)
				assert.Empty(t, got.body, name)
			}
			require.Contains(t, entries, rootFSArchiveName(file))
			got := entries[rootFSArchiveName(file)]
			assert.Equal(t, byte(tar.TypeReg), got.header.Typeflag)
			assert.Equal(t, "library contents", got.body)
			if tt.directory {
				for _, alias := range []string{"alias", "intermediate"} {
					prefix := rootFSArchiveName(filepath.Join(dir, alias)) + "/"
					for name := range entries {
						assert.False(t, strings.HasPrefix(name, prefix), "entry under a symlink instead of its target: %s", name)
					}
				}
			}
		})
	}
}

func TestRootFSRejectsInvalidPaths(t *testing.T) {
	root := New(io.Discard)
	assert.Error(t, root.Copy("relative/file"))
	assert.ErrorIs(t, root.Copy(filepath.Join(rootFSTempDir(t), "missing")), os.ErrNotExist)
	require.NoError(t, root.Close())
}

func TestRootFSCircularSymlinks(t *testing.T) {
	for _, self := range []bool{false, true} {
		name := "two links"
		if self {
			name = "self link"
		}
		t.Run(name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			target := "b"
			if self {
				target = "a"
			} else {
				require.NoError(t, os.Symlink("a", filepath.Join(dir, "b")))
			}
			require.NoError(t, os.Symlink(target, filepath.Join(dir, "a")))
			var out bytes.Buffer
			root := New(&out)
			for range 2 {
				require.ErrorContains(t, root.Copy(filepath.Join(dir, "a")), "circular symlink")
			}
			file := filepath.Join(dir, "file")
			writeRootFSFile(t, file, "after error", 0644)
			require.NoError(t, root.Copy(file))
			require.NoError(t, root.Close())
			assert.Equal(t, "after error", readRootFSTar(t, out.Bytes())[rootFSArchiveName(file)].body)
		})
	}
}

type rootFSFailWriter struct{ err error }

func (w *rootFSFailWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}

func TestRootFSWriteErrors(t *testing.T) {
	want := errors.New("write failed")
	t.Run("Copy", func(t *testing.T) {
		root := New(&rootFSFailWriter{err: want})
		assert.ErrorIs(t, root.Copy(rootFSTempDir(t)), want)
	})
	t.Run("Close", func(t *testing.T) {
		writer := &rootFSFailWriter{}
		root := New(writer)
		file := filepath.Join(rootFSTempDir(t), "file")
		writeRootFSFile(t, file, "content", 0644)
		require.NoError(t, root.Copy(file))
		writer.err = want
		assert.ErrorIs(t, root.Close(), want)
	})
}
