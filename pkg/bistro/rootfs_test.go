package bistro

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
			root := NewRootFS(&out)
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
			root := NewRootFS(tt.writer)
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
	root := NewRootFS(&out)
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
			root := NewRootFS(&out)
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

func TestRootFSSpecialFilePattern(t *testing.T) {
	pat := NewRootFS(io.Discard).(*rootFS).spFilePat
	for _, name := range []string{"/proc", "/proc/self/fd/1", "/sys", "/sys/class/net"} {
		assert.True(t, pat.MatchString(name), name)
	}
	for _, name := range []string{"/", "/processes", "/system", "/sysroot/lib", "/usr/proc", "/dev/pts/0"} {
		assert.False(t, pat.MatchString(name), name)
	}
}

func TestRootFSStopsAtSpecialFileSymlinks(t *testing.T) {
	dir := rootFSTempDir(t)
	// /proc/self/fd/1 may point to a non-path target such as "pipe:[1]".
	special := filepath.Join(dir, "proc", "self", "fd", "1")
	require.NoError(t, os.MkdirAll(filepath.Dir(special), 0755))
	require.NoError(t, os.Symlink("pipe:[1]", special))
	// A sibling sharing the prefix must still be followed.
	file := filepath.Join(dir, "processes", "file")
	writeRootFSFile(t, file, "not special", 0644)
	links := map[string]string{
		filepath.Join(dir, "dev", "stdout"): special,
		filepath.Join(dir, "dev", "stderr"): filepath.Join("..", "proc", "self", "fd", "2"),
		filepath.Join(dir, "dev", "procs"):  filepath.Join("..", "processes"),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dev"), 0755))
	for name, target := range links {
		require.NoError(t, os.Symlink(target, name))
	}

	var out bytes.Buffer
	root := NewRootFS(&out).(*rootFS)
	root.spFilePat = regexp.MustCompile("^" + regexp.QuoteMeta(dir) + `/(proc|sys)(/|$)`)
	for _, name := range []string{
		filepath.Join(dir, "dev", "stdout"),
		filepath.Join(dir, "dev", "stderr"),
		filepath.Join(dir, "dev", "procs", "file"),
	} {
		require.NoError(t, root.Copy(name))
	}
	require.NoError(t, root.Close())

	entries := readRootFSTar(t, out.Bytes())
	for name, target := range links {
		require.Contains(t, entries, rootFSArchiveName(name))
		got := entries[rootFSArchiveName(name)]
		assert.Equal(t, byte(tar.TypeSymlink), got.header.Typeflag, name)
		assert.Equal(t, target, got.header.Linkname, name)
	}
	procPrefix := rootFSArchiveName(filepath.Join(dir, "proc"))
	for name := range entries {
		assert.False(t, name == procPrefix || strings.HasPrefix(name, procPrefix+"/"), "entry under special path: %s", name)
	}
	require.Contains(t, entries, rootFSArchiveName(file))
	assert.Equal(t, "not special", entries[rootFSArchiveName(file)].body)
}

func TestRootFSRejectsInvalidPaths(t *testing.T) {
	root := NewRootFS(io.Discard)
	assert.EqualError(t, root.Copy("relative/file"), "path must be absolute")
	assert.ErrorIs(t, root.Copy(filepath.Join(rootFSTempDir(t), "missing")), os.ErrNotExist)
	require.NoError(t, root.Close())
}

func TestRootFSRepeatedSymlink(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "file")
	link := filepath.Join(dir, "selfdir")
	writeRootFSFile(t, file, "resolved content", 0644)
	require.NoError(t, os.Symlink(".", link))

	var out bytes.Buffer
	root := NewRootFS(&out)
	for range 2 {
		require.NoError(t, root.Copy(filepath.Join(dir, "selfdir", "selfdir", "file")))
	}
	require.NoError(t, root.Close())

	entries := readRootFSTar(t, out.Bytes())
	require.Contains(t, entries, rootFSArchiveName(file))
	assert.Equal(t, "resolved content", entries[rootFSArchiveName(file)].body)
	require.Contains(t, entries, rootFSArchiveName(link))
	assert.Equal(t, byte(tar.TypeSymlink), entries[rootFSArchiveName(link)].header.Typeflag)
	assert.Equal(t, ".", entries[rootFSArchiveName(link)].header.Linkname)
	for name := range entries {
		assert.False(t, strings.HasPrefix(name, rootFSArchiveName(link)+"/"), "entry under symlink: %s", name)
	}
}

func TestRootFSCircularSymlinks(t *testing.T) {
	for _, tt := range []struct {
		name, target, wantErr string
	}{
		{name: "two links", target: "b", wantErr: "circular symlink"},
		{name: "self link", target: "a", wantErr: "circular symlink"},
		{name: "growing remaining path", target: "a/subdir", wantErr: "too many symlinks"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			if tt.target == "b" {
				require.NoError(t, os.Symlink("a", filepath.Join(dir, "b")))
			}
			require.NoError(t, os.Symlink(tt.target, filepath.Join(dir, "a")))
			var out bytes.Buffer
			root := NewRootFS(&out)
			for range 2 {
				require.ErrorContains(t, root.Copy(filepath.Join(dir, "a")), tt.wantErr)
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
		root := NewRootFS(&rootFSFailWriter{err: want})
		assert.ErrorIs(t, root.Copy(rootFSTempDir(t)), want)
	})
	t.Run("Close", func(t *testing.T) {
		writer := &rootFSFailWriter{}
		root := NewRootFS(writer)
		file := filepath.Join(rootFSTempDir(t), "file")
		writeRootFSFile(t, file, "content", 0644)
		require.NoError(t, root.Copy(file))
		writer.err = want
		assert.ErrorIs(t, root.Close(), want)
	})
}
