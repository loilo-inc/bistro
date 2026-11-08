package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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

func TestArchiverAdd(t *testing.T) {
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
			root := NewArchiver(os.DirFS("/"), &out)
			require.NoError(t, root.Add(MustPath(tt.path), tt.content, tt.mode))
			// A following entry verifies that the content size and tar padding
			// do not corrupt the next file.
			require.NoError(t, root.Add(MustPath("next"), []byte("next file"), 0644))
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

func TestArchiverAddWriteErrors(t *testing.T) {
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
			root := NewArchiver(os.DirFS("/"), tt.writer)
			assert.ErrorIs(t, root.Add(MustPath("file"), content, 0644), want)
		})
	}
}

func TestArchiverFilesAndDirectories(t *testing.T) {
	dir := rootFSTempDir(t)
	first := filepath.Join(dir, "bin", "tool")
	second := filepath.Join(dir, "share", "tool")
	writeRootFSFile(t, first, "executable\n", 0751)
	writeRootFSFile(t, second, "data\n", 0640)
	require.NoError(t, os.Chmod(filepath.Dir(first), 0750))
	var out bytes.Buffer
	root := NewArchiver(os.DirFS("/"), &out)
	for _, name := range []string{first, second, first, filepath.Dir(first)} {
		require.NoError(t, root.Copy(MustPath(name)))
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

func TestArchiverSymlinks(t *testing.T) {
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
			root := NewArchiver(os.DirFS("/"), &out)
			for _, name := range []string{requested, requested, file} {
				require.NoError(t, root.Copy(MustPath(name)))
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

func TestArchiverStopsAtSpecialFileSymlinks(t *testing.T) {
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
	root := NewArchiver(os.DirFS("/"), &out).(*archiver)
	root.SetExcludes(NewPathSet(MustPaths(filepath.Join(dir, "proc"), filepath.Join(dir, "sys"))))
	for _, name := range []string{
		filepath.Join(dir, "dev", "stdout"),
		filepath.Join(dir, "dev", "stderr"),
		filepath.Join(dir, "dev", "procs", "file"),
	} {
		require.NoError(t, root.Copy(MustPath(name)))
	}
	require.NoError(t, root.Close())

	entries := readRootFSTar(t, out.Bytes())
	for name, target := range links {
		if name != filepath.Join(dir, "dev", "procs") {
			require.NotContains(t, entries, rootFSArchiveName(name), "links into excluded subtrees must be skipped")
			continue
		}
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

func TestArchiverMissingPath(t *testing.T) {
	root := NewArchiver(os.DirFS("/"), io.Discard)
	assert.ErrorIs(t, root.Copy(MustPath(filepath.Join(rootFSTempDir(t), "missing"))), os.ErrNotExist)
	require.NoError(t, root.Close())
}

func TestArchiverRepeatedSymlink(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "file")
	link := filepath.Join(dir, "selfdir")
	writeRootFSFile(t, file, "resolved content", 0644)
	require.NoError(t, os.Symlink(".", link))

	var out bytes.Buffer
	root := NewArchiver(os.DirFS("/"), &out)
	for range 2 {
		require.NoError(t, root.Copy(MustPath(filepath.Join(dir, "selfdir", "selfdir", "file"))))
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

func TestArchiverSymlinkRootBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, remaining string
		escape          bool
	}{
		{name: "parent traversal to root"},
		{name: "parent traversal to root with child", remaining: "child"},
		{name: "parent traversal above root", escape: true},
		{name: "parent traversal above root with child", escape: true, remaining: "child"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			target := filepath.Join(dir, "target")
			child := filepath.Join(target, "child")
			writeRootFSFile(t, child, "target contents", 0644)
			depth := len(strings.Split(strings.TrimPrefix(dir, "/"), "/"))
			if tc.escape {
				depth++
			}
			linkTarget := strings.Repeat("../", depth) + strings.TrimPrefix(target, "/")
			link := filepath.Join(dir, "link")
			require.NoError(t, os.Symlink(linkTarget, link))
			// The host accepts both links by clamping excess '..' at '/'.
			// The archive must reject traversal above its root explicitly.
			_, err := os.Stat(filepath.Join(link, tc.remaining))
			require.NoError(t, err)

			var out bytes.Buffer
			root := NewArchiver(os.DirFS("/"), &out).(*archiver)
			for range 2 {
				err := root.Copy(MustPath(link).Join(tc.remaining))
				if tc.escape {
					require.ErrorContains(t, err, "upper root symlink detected")
				} else {
					require.NoError(t, err)
				}
			}
			valid := filepath.Join(dir, "valid")
			writeRootFSFile(t, valid, "after resolution", 0644)
			require.NoError(t, root.Copy(MustPath(valid)))
			require.NoError(t, root.Close())
			entries := readRootFSTar(t, out.Bytes())
			require.Equal(t, "after resolution", entries[rootFSArchiveName(valid)].body)
			if tc.escape {
				require.NotContains(t, entries, rootFSArchiveName(target))
				require.NotContains(t, entries, rootFSArchiveName(child))
			} else {
				require.Contains(t, entries, rootFSArchiveName(target))
				if tc.remaining != "" {
					require.Equal(t, "target contents", entries[rootFSArchiveName(child)].body)
				}
			}
		})
	}
}

func TestArchiverCircularSymlinks(t *testing.T) {
	for _, tt := range []struct {
		name, target, wantErr string
	}{
		{name: "two links", target: "b", wantErr: "circular symlink"},
		{name: "self link", target: "a", wantErr: "circular symlink"},
		{name: "growing remaining path", target: "a/subdir", wantErr: "circular symlink"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := rootFSTempDir(t)
			if tt.target == "b" {
				require.NoError(t, os.Symlink("a", filepath.Join(dir, "b")))
			}
			require.NoError(t, os.Symlink(tt.target, filepath.Join(dir, "a")))
			var out bytes.Buffer
			root := NewArchiver(os.DirFS("/"), &out)
			for range 2 {
				require.ErrorContains(t, root.Copy(MustPath(filepath.Join(dir, "a"))), tt.wantErr)
			}
			file := filepath.Join(dir, "file")
			writeRootFSFile(t, file, "after error", 0644)
			require.NoError(t, root.Copy(MustPath(file)))
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

func TestArchiverWriteErrors(t *testing.T) {
	want := errors.New("write failed")
	t.Run("Copy", func(t *testing.T) {
		root := NewArchiver(os.DirFS("/"), &rootFSFailWriter{err: want})
		assert.ErrorIs(t, root.Copy(MustPath(rootFSTempDir(t))), want)
	})
	t.Run("Close", func(t *testing.T) {
		writer := &rootFSFailWriter{}
		root := NewArchiver(os.DirFS("/"), writer)
		file := filepath.Join(rootFSTempDir(t), "file")
		writeRootFSFile(t, file, "content", 0644)
		require.NoError(t, root.Copy(MustPath(file)))
		writer.err = want
		assert.ErrorIs(t, root.Close(), want)
	})
}

func TestArchiverExcludesAliasesAndAddedFiles(t *testing.T) {
	dir := rootFSTempDir(t)
	real := filepath.Join(dir, "real")
	omitted := filepath.Join(real, "omitted")
	alias := filepath.Join(dir, "alias")
	writeRootFSFile(t, omitted, "omit", 0644)
	require.NoError(t, os.Symlink("real", alias))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink("real/omitted", link))
	var out bytes.Buffer
	a := NewArchiver(os.DirFS("/"), &out)
	a.SetExcludes(NewPathSet(MustPaths(filepath.Join(alias, "omitted"), "/var/lib/dpkg")))
	require.NoError(t, a.Copy(MustPath(link)))
	require.NoError(t, a.Copy(MustPath(omitted)))
	require.NoError(t, a.Add(MustPath("/var/lib/dpkg/status.d/example"), []byte("metadata"), 0644))
	require.NoError(t, a.Close())
	entries := readRootFSTar(t, out.Bytes())
	require.NotContains(t, entries, rootFSArchiveName(omitted))
	require.NotContains(t, entries, "var/lib/dpkg/status.d/example")
}

// Bound filesystem work so a traversal regression fails instead of recursing forever.
type traversalBudgetFS struct {
	fs.FS
	calls int
}

func (f *traversalBudgetFS) Lstat(name string) (fs.FileInfo, error) {
	f.calls++
	if f.calls > 2000 {
		return nil, errors.New("traversal exceeded filesystem operation budget")
	}
	return fs.Lstat(f.FS, name)
}

func (f *traversalBudgetFS) ReadLink(name string) (string, error) {
	return fs.ReadLink(f.FS, name)
}

func TestArchiverCopyRecursive(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		copyFirst   bool
		links       map[string]string
	}{
		{name: "directory", input: "/tree"},
		{name: "directory alias", input: "/alias", links: map[string]string{"alias": "tree"}},
		{name: "after nonrecursive copy", input: "/tree", copyFirst: true},
		{name: "alias after nonrecursive copy", input: "/alias", copyFirst: true, links: map[string]string{"alias": "tree"}},
		{name: "self directory link", input: "/tree", links: map[string]string{"tree/self": "."}},
		{name: "mutual directory links", input: "/tree", links: map[string]string{"tree/outside": "../outside", "outside/back": "../tree"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{
				"tree/config":       {Data: []byte("configuration"), Mode: 0640},
				"tree/nested/asset": {Data: []byte("asset")},
				"tree/empty":        {Mode: fs.ModeDir | 0750},
				"outside/data":      {Data: []byte("external data")},
			}
			for name, target := range tc.links {
				files[name] = &fstest.MapFile{Mode: fs.ModeSymlink | 0777, Data: []byte(target)}
			}
			fss := &traversalBudgetFS{FS: files}
			var out bytes.Buffer
			a := NewArchiver(fss, &out)
			if tc.copyFirst {
				require.NoError(t, a.Copy(MustPath(tc.input)))
			}
			require.NoError(t, a.CopyRecursive(MustPath(tc.input)))
			require.NoError(t, a.CopyRecursive(MustPath(tc.input)))
			require.NoError(t, a.Close())
			entries := readRootFSTar(t, out.Bytes())
			require.Equal(t, "configuration", entries["tree/config"].body)
			require.Equal(t, int64(0640), entries["tree/config"].header.Mode)
			require.Equal(t, "asset", entries["tree/nested/asset"].body)
			require.Contains(t, entries, "tree/empty")
			require.Equal(t, byte(tar.TypeDir), entries["tree/empty"].header.Typeflag)
			for name, target := range tc.links {
				require.Contains(t, entries, name)
				require.Equal(t, byte(tar.TypeSymlink), entries[name].header.Typeflag)
				require.Equal(t, target, entries[name].header.Linkname)
			}
			if tc.name == "mutual directory links" {
				require.Equal(t, "external data", entries["outside/data"].body)
			} else {
				require.NotContains(t, entries, "outside/data")
			}
		})
	}
}

func TestArchiverCopyRecursivePreservesAdditionalAliases(t *testing.T) {
	fss := fstest.MapFS{
		"tree/config": {Data: []byte("configuration")},
		"first":       {Mode: fs.ModeSymlink | 0777, Data: []byte("tree")},
		"second":      {Mode: fs.ModeSymlink | 0777, Data: []byte("tree")},
	}
	var out bytes.Buffer
	a := NewArchiver(fss, &out)
	for _, name := range []string{"/tree", "/first", "/second"} {
		require.NoError(t, a.CopyRecursive(MustPath(name)))
	}
	require.NoError(t, a.Close())
	entries := readRootFSTar(t, out.Bytes())
	require.Equal(t, "configuration", entries["tree/config"].body)
	for _, name := range []string{"first", "second"} {
		require.Contains(t, entries, name)
		require.Equal(t, "tree", entries[name].header.Linkname)
	}
}

func TestArchiverCopyRecursiveExcludesSubtrees(t *testing.T) {
	fss := fstest.MapFS{
		"tree/config":          {Data: []byte("keep")},
		"tree/cache/asset":     {Data: []byte("omit")},
		"tree/cache-old/asset": {Data: []byte("keep sibling")},
		"alias":                {Mode: fs.ModeSymlink | 0777, Data: []byte("tree")},
	}
	var out bytes.Buffer
	a := NewArchiver(fss, &out)
	a.SetExcludes(NewPathSet(MustPaths("/alias/cache")))
	require.NoError(t, a.CopyRecursive(MustPath("/alias")))
	require.NoError(t, a.Close())
	entries := readRootFSTar(t, out.Bytes())
	require.Equal(t, "keep", entries["tree/config"].body)
	require.Equal(t, "keep sibling", entries["tree/cache-old/asset"].body)
	require.NotContains(t, entries, "tree/cache")
	require.NotContains(t, entries, "tree/cache/asset")
}

// Reject reads of special files so this test cannot block on a FIFO.
type specialFileReadGuardFS struct {
	fs.FS
	specialOpens int
}

func (f *specialFileReadGuardFS) Lstat(name string) (fs.FileInfo, error) {
	return fs.Lstat(f.FS, name)
}

func (f *specialFileReadGuardFS) ReadLink(name string) (string, error) {
	return fs.ReadLink(f.FS, name)
}

func (f *specialFileReadGuardFS) Open(name string) (fs.File, error) {
	info, err := fs.Stat(f.FS, name)
	if err != nil {
		return nil, err
	}
	mode := info.Mode()
	if !mode.IsDir() && !mode.IsRegular() {
		f.specialOpens++
		return nil, errors.New("special file contents must not be opened")
	}
	return f.FS.Open(name)
}

func TestArchiverSkipsSpecialFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode fs.FileMode
	}{
		{name: "FIFO", mode: fs.ModeNamedPipe},
		{name: "socket", mode: fs.ModeSocket},
		{name: "block device", mode: fs.ModeDevice},
		{name: "character device", mode: fs.ModeDevice | fs.ModeCharDevice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, recursive := range []bool{false, true} {
				name := "Copy"
				if recursive {
					name = "CopyRecursive"
				}
				t.Run(name, func(t *testing.T) {
					fss := &specialFileReadGuardFS{FS: fstest.MapFS{
						"tree/special":      {Mode: tc.mode | 0600},
						"tree/file":         {Data: []byte("asset"), Mode: 0640},
						"tree/empty":        {Mode: fs.ModeDir | 0750},
						"tree/link":         {Mode: fs.ModeSymlink | 0777, Data: []byte("file")},
						"tree/special-link": {Mode: fs.ModeSymlink | 0777, Data: []byte("special")},
					}}
					var out bytes.Buffer
					a := NewArchiver(fss, &out)
					if recursive {
						require.NoError(t, a.CopyRecursive(MustPath("/tree")))
					} else {
						for _, path := range []string{"/tree/special", "/tree/file", "/tree/empty", "/tree/link", "/tree/special-link"} {
							require.NoError(t, a.Copy(MustPath(path)))
						}
					}
					require.NoError(t, a.Close())
					entries := readRootFSTar(t, out.Bytes())
					require.NotContains(t, entries, "tree/special", "special files must not have archive headers")
					require.Zero(t, fss.specialOpens)
					require.Equal(t, "asset", entries["tree/file"].body)
					require.Equal(t, int64(0640), entries["tree/file"].header.Mode)
					require.Contains(t, entries, "tree/empty")
					require.Equal(t, byte(tar.TypeDir), entries["tree/empty"].header.Typeflag)
					require.Equal(t, int64(0750), entries["tree/empty"].header.Mode)
					for _, link := range []struct{ name, target string }{{"tree/link", "file"}, {"tree/special-link", "special"}} {
						require.Contains(t, entries, link.name, "symlink headers must remain intact")
						require.Equal(t, byte(tar.TypeSymlink), entries[link.name].header.Typeflag)
						require.Equal(t, link.target, entries[link.name].header.Linkname)
					}
				})
			}
		})
	}
}
