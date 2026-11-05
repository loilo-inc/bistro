package bistro

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/testutil"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unpackFile(name, body string, mode int64) rootFSEntry {
	return rootFSEntry{header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode}, body: body}
}

func unpackDir(name string, mode int64) rootFSEntry {
	return rootFSEntry{header: tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: mode}}
}

func unpackLink(name, target string) rootFSEntry {
	return rootFSEntry{header: tar.Header{Name: name, Typeflag: tar.TypeSymlink, Mode: 0777, Linkname: target}}
}

func writeUnpackTar(t *testing.T, entries ...rootFSEntry) string {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, entry := range entries {
		hdr := entry.header
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(entry.body))
		}
		require.NoError(t, w.WriteHeader(&hdr))
		if hdr.Typeflag == tar.TypeReg {
			_, err := io.WriteString(w, entry.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, w.Close())
	name := filepath.Join(t.TempDir(), "archive.tar")
	require.NoError(t, os.WriteFile(name, buf.Bytes(), 0600))
	return name
}

func requireUnpackedFile(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
	info, err := os.Lstat(name)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	assert.Equal(t, mode, info.Mode().Perm())
}

func TestUnpackerExtractsArchiverOutput(t *testing.T) {
	testutil.RequireRoot(t)
	base := rootFSTempDir(t)
	source := filepath.Join(base, "source")
	writeRootFSFile(t, filepath.Join(source, "tool"), "executable", 0751)
	require.NoError(t, os.Symlink("tool", filepath.Join(source, "link")))
	var buf bytes.Buffer
	archiver := rootfs.NewArchiver(os.DirFS("/"), &buf)
	for _, name := range []string{"tool", "link"} {
		require.NoError(t, archiver.Copy(rootfs.MustPath(filepath.Join(source, name))))
	}
	require.NoError(t, archiver.Close())
	archive := filepath.Join(base, "archive.tar")
	require.NoError(t, os.WriteFile(archive, buf.Bytes(), 0600))
	dest := filepath.Join(base, "missing", "rootfs")
	require.NoError(t, NewUnpacker(dest).Unpack(archive))
	extracted := filepath.Join(dest, rootFSArchiveName(source))
	requireUnpackedFile(t, filepath.Join(extracted, "tool"), "executable", 0751)
	link, err := os.Readlink(filepath.Join(extracted, "link"))
	require.NoError(t, err)
	assert.Equal(t, "tool", link)
}

func TestUnpackerReplacesEntries(t *testing.T) {
	testutil.RequireRoot(t)
	entries := map[string]rootFSEntry{
		"file":    unpackFile("entry", "new", 0640),
		"dir":     unpackDir("entry", 0700),
		"symlink": unpackLink("entry", "new-target"),
	}
	for oldKind, old := range entries {
		for newKind, next := range entries {
			t.Run(oldKind+" to "+newKind, func(t *testing.T) {
				initial := old
				initial.body = "long original content"
				if oldKind == "file" {
					initial.header.Mode = 0755
				}
				if oldKind == "symlink" {
					initial.header.Linkname = "old-target"
				}
				archiveEntries := []rootFSEntry{initial}
				if oldKind == "dir" && newKind == "dir" {
					archiveEntries = append(archiveEntries, unpackFile("entry/child", "retained", 0644))
				}
				archiveEntries = append(archiveEntries, next)
				dest := filepath.Join(t.TempDir(), "rootfs")
				require.NoError(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, archiveEntries...)))
				target := filepath.Join(dest, "entry")
				info, err := os.Lstat(target)
				require.NoError(t, err)
				switch newKind {
				case "file":
					requireUnpackedFile(t, target, "new", 0640)
				case "dir":
					require.True(t, info.IsDir())
					assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
					if oldKind == "dir" {
						requireUnpackedFile(t, filepath.Join(target, "child"), "retained", 0644)
					}
				case "symlink":
					link, err := os.Readlink(target)
					require.NoError(t, err)
					assert.Equal(t, "new-target", link)
				}
			})
		}
	}
}

func TestUnpackerPreservesNonemptyDirectory(t *testing.T) {
	testutil.RequireRoot(t)
	for _, replacement := range []rootFSEntry{unpackFile("entry", "new", 0644), unpackLink("entry", "target")} {
		t.Run(fmt.Sprint(replacement.header.Typeflag), func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "rootfs")
			archive := writeUnpackTar(t, unpackDir("entry", 0700), unpackFile("entry/child", "original", 0644), replacement)
			require.Error(t, NewUnpacker(dest).Unpack(archive))
			requireUnpackedFile(t, filepath.Join(dest, "entry", "child"), "original", 0644)
		})
	}
}

func TestUnpackerDirectoryModes(t *testing.T) {
	testutil.RequireRoot(t)
	for _, mode := range []int64{0750, 0555, 0000} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "rootfs")
			target := filepath.Join(dest, "entry")
			t.Cleanup(func() { _ = os.Chmod(target, 0700) })
			require.NoError(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, unpackDir("entry/", mode))))
			info, err := os.Stat(target)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(mode), info.Mode().Perm())
		})
	}
}

func TestUnpackerAppliesExistingDirectoryAttributes(t *testing.T) {
	testutil.RequireRoot(t)
	dest := t.TempDir()
	existing := filepath.Join(dest, "existing")
	require.NoError(t, os.Mkdir(existing, 0700))
	require.NoError(t, os.Chmod(existing, 0700))
	directory := unpackDir("existing", 0750)
	directory.header.Uid, directory.header.Gid = 12345, 23456
	implicit := unpackDir("implicit/", 02770)
	implicit.header.Uid, implicit.header.Gid = 34567, 45678
	archive := writeUnpackTar(t,
		directory,
		unpackFile("existing/child", "directory contents", 0640),
		unpackFile("./implicit/child", "implicit parent", 0644),
		implicit,
	)
	require.NoError(t, NewUnpacker(dest).Unpack(archive))
	for _, entry := range []struct {
		name     string
		mode     os.FileMode
		uid, gid uint32
	}{
		{"existing", 0750, 12345, 23456}, {"implicit", os.ModeSetgid | 0770, 34567, 45678},
	} {
		name := filepath.Join(dest, entry.name)
		info, err := os.Lstat(name)
		require.NoError(t, err)
		assert.Equal(t, entry.mode, info.Mode()&(os.ModePerm|os.ModeSetgid), entry.name)
		stat := info.Sys().(*syscall.Stat_t)
		assert.Equal(t, entry.uid, stat.Uid, entry.name)
		assert.Equal(t, entry.gid, stat.Gid, entry.name)
	}
	requireUnpackedFile(t, filepath.Join(existing, "child"), "directory contents", 0640)
	requireUnpackedFile(t, filepath.Join(dest, "implicit", "child"), "implicit parent", 0644)
}

func TestUnpackerPreservesDestinationAttributes(t *testing.T) {
	testutil.RequireRoot(t)
	for _, name := range []string{".", "./", "nested/.."} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			require.NoError(t, os.Chown(dest, 10001, 10002))
			require.NoError(t, os.Chmod(dest, 0750))
			entry := unpackDir(name, 0000)
			entry.header.Uid, entry.header.Gid = 12345, 23456
			archive := writeUnpackTar(t, entry, unpackFile("file", "data", 0644))
			require.NoError(t, NewUnpacker(dest).Unpack(archive))
			info, err := os.Stat(dest)
			require.NoError(t, err)
			stat := info.Sys().(*syscall.Stat_t)
			assert.Equal(t, uint32(10001), stat.Uid)
			assert.Equal(t, uint32(10002), stat.Gid)
			assert.Equal(t, os.FileMode(0750), info.Mode().Perm())
			requireUnpackedFile(t, filepath.Join(dest, "file"), "data", 0644)
		})
	}
}

func TestUnpackerRestoresFileOwnerAndSpecialModes(t *testing.T) {
	testutil.RequireRoot(t)
	dest := t.TempDir()
	entry := unpackFile("executable", "data", 06755)
	entry.header.Uid, entry.header.Gid = 12345, 23456
	require.NoError(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, entry)))
	info, err := os.Lstat(filepath.Join(dest, "executable"))
	require.NoError(t, err)
	stat := info.Sys().(*syscall.Stat_t)
	assert.Equal(t, uint32(12345), stat.Uid)
	assert.Equal(t, uint32(23456), stat.Gid)
	assert.Equal(t, os.ModeSetuid|os.ModeSetgid|0755, info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid))
}

func TestUnpackerRestoresSymlinkOwnerWithoutFollowingTarget(t *testing.T) {
	testutil.RequireRoot(t)
	for _, targetKind := range []string{"internal", "external", "dangling"} {
		t.Run(targetKind, func(t *testing.T) {
			base := t.TempDir()
			dest := filepath.Join(base, "rootfs")
			target := "target"
			var entries []rootFSEntry
			if targetKind == "internal" {
				entry := unpackFile("target", "original", 0644)
				entry.header.Uid, entry.header.Gid = 34567, 45678
				entries = append(entries, entry)
			} else if targetKind == "external" {
				target = filepath.Join(base, "external")
				require.NoError(t, os.WriteFile(target, []byte("original"), 0644))
				require.NoError(t, os.Chown(target, 34567, 45678))
			}
			link := unpackLink("link", target)
			link.header.Uid, link.header.Gid = 12345, 23456
			entries = append(entries, link)
			require.NoError(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, entries...)))
			name := filepath.Join(dest, "link")
			info, err := os.Lstat(name)
			require.NoError(t, err)
			stat := info.Sys().(*syscall.Stat_t)
			assert.Equal(t, uint32(12345), stat.Uid)
			assert.Equal(t, uint32(23456), stat.Gid)
			actual, err := os.Readlink(name)
			require.NoError(t, err)
			assert.Equal(t, target, actual)
			if targetKind == "dangling" {
				return
			}
			if targetKind == "internal" {
				target = filepath.Join(dest, target)
			}
			info, err = os.Stat(target)
			require.NoError(t, err)
			stat = info.Sys().(*syscall.Stat_t)
			assert.Equal(t, uint32(34567), stat.Uid)
			assert.Equal(t, uint32(45678), stat.Gid)
			requireUnpackedFile(t, target, "original", 0644)
		})
	}
}

func TestUnpackerInternalSymlinkDirectoryReplacement(t *testing.T) {
	testutil.RequireRoot(t)
	dest := filepath.Join(t.TempDir(), "rootfs")
	archive := writeUnpackTar(t, unpackDir("entry", 0555), unpackLink("alias", "."), unpackFile("alias/entry", "replacement", 0640))
	require.NoError(t, NewUnpacker(dest).Unpack(archive))
	requireUnpackedFile(t, filepath.Join(dest, "entry"), "replacement", 0640)
}

func TestUnpackerExtractsThroughInternalSymlink(t *testing.T) {
	testutil.RequireRoot(t)
	dest := filepath.Join(t.TempDir(), "rootfs")
	archive := writeUnpackTar(t, unpackDir("usr/lib", 0755), unpackLink("lib", "usr/lib"), unpackFile("lib/example.so", "library", 0644))
	require.NoError(t, NewUnpacker(dest).Unpack(archive))
	requireUnpackedFile(t, filepath.Join(dest, "usr", "lib", "example.so"), "library", 0644)
	link, err := os.Readlink(filepath.Join(dest, "lib"))
	require.NoError(t, err)
	assert.Equal(t, "usr/lib", link)
}

func TestUnpackerMultipleArchives(t *testing.T) {
	testutil.RequireRoot(t)
	dest := filepath.Join(t.TempDir(), "rootfs")
	u := NewUnpacker(dest)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dest, "shared"), 0700) })
	first := writeUnpackTar(t, unpackDir("shared", 0700), unpackFile("shared/file", "long original content", 0755), unpackLink("link", "shared/file"))
	require.NoError(t, u.Unpack(first))
	second := writeUnpackTar(t, unpackDir("shared", 0700), unpackFile("shared/file", "new", 0644), unpackFile("shared/extra", "extra", 0600), unpackLink("link", "shared/file"))
	require.NoError(t, u.Unpack(second))
	requireUnpackedFile(t, filepath.Join(dest, "shared", "file"), "new", 0644)
	requireUnpackedFile(t, filepath.Join(dest, "shared", "extra"), "extra", 0600)
	info, err := os.Stat(filepath.Join(dest, "shared"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
	link, err := os.Readlink(filepath.Join(dest, "link"))
	require.NoError(t, err)
	assert.Equal(t, "shared/file", link)
}

func TestUnpackerRejectsEscapingPaths(t *testing.T) {
	testutil.RequireRoot(t)
	for _, name := range []string{"../outside", "nested/../../outside", "/absolute"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			outside := filepath.Join(base, "outside")
			writeRootFSFile(t, outside, "original", 0644)
			if name == "/absolute" {
				name = outside
			}
			archive := writeUnpackTar(t, unpackFile(name, "overwritten", 0644))
			require.ErrorContains(t, NewUnpacker(filepath.Join(base, "rootfs")).Unpack(archive), "invalid target path")
			requireUnpackedFile(t, outside, "original", 0644)
		})
	}
}

func TestUnpackerRejectsSymlinkEscapes(t *testing.T) {
	testutil.RequireRoot(t)
	for _, existing := range []bool{false, true} {
		for _, absolute := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/absolute=%t", existing, absolute), func(t *testing.T) {
				base := rootFSTempDir(t)
				dest, outside := filepath.Join(base, "rootfs"), filepath.Join(base, "outside")
				writeRootFSFile(t, filepath.Join(outside, "victim"), "original", 0644)
				target := "../outside"
				if absolute {
					target = outside
				}
				var entries []rootFSEntry
				if existing {
					require.NoError(t, os.Mkdir(dest, 0755))
					require.NoError(t, os.Symlink(target, filepath.Join(dest, "link")))
				} else {
					entries = append(entries, unpackLink("link", target))
				}
				entries = append(entries, unpackFile("link/victim", "overwritten", 0644))
				require.Error(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, entries...)))
				requireUnpackedFile(t, filepath.Join(outside, "victim"), "original", 0644)
			})
		}
	}
}

func TestUnpackerReplacesSymlinkWithoutFollowingIt(t *testing.T) {
	testutil.RequireRoot(t)
	base := rootFSTempDir(t)
	outside := filepath.Join(base, "outside")
	writeRootFSFile(t, outside, "original", 0644)
	dest := filepath.Join(base, "rootfs")
	archive := writeUnpackTar(t, unpackLink("entry", outside), unpackFile("entry", "new", 0600))
	require.NoError(t, NewUnpacker(dest).Unpack(archive))
	requireUnpackedFile(t, filepath.Join(dest, "entry"), "new", 0600)
	requireUnpackedFile(t, outside, "original", 0644)
}

func TestUnpackerUnsupportedEntryPreservesFileAndDirectoryMode(t *testing.T) {
	testutil.RequireRoot(t)
	for _, kind := range []byte{tar.TypeLink, tar.TypeFifo} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "rootfs")
			u := NewUnpacker(dest)
			archive := writeUnpackTar(t, unpackDir("readonly", 0555), unpackFile("victim", "original", 0644), rootFSEntry{header: tar.Header{Name: "victim", Typeflag: kind, Mode: 0644, Linkname: "missing"}})
			require.ErrorContains(t, u.Unpack(archive), "unsupported tar entry type")
			requireUnpackedFile(t, filepath.Join(dest, "victim"), "original", 0644)
			info, err := os.Stat(filepath.Join(dest, "readonly"))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0555), info.Mode().Perm())
			require.NoError(t, os.Remove(filepath.Join(dest, "readonly")))
			// A failed archive must not prevent the next extraction.
			require.NoError(t, u.Unpack(writeUnpackTar(t, unpackFile("next", "ok", 0644))))
		})
	}
}

func TestUnpackerTruncatedFilePreservesDirectoryMode(t *testing.T) {
	testutil.RequireRoot(t)
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "readonly", Typeflag: tar.TypeDir, Mode: 0555}))
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "partial", Typeflag: tar.TypeReg, Mode: 0644, Size: 10}))
	_, err := w.Write([]byte("short"))
	require.NoError(t, err)
	// Leave the declared file body incomplete rather than finalizing the tar.
	archive := filepath.Join(t.TempDir(), "truncated.tar")
	require.NoError(t, os.WriteFile(archive, buf.Bytes(), 0600))
	dest := filepath.Join(t.TempDir(), "rootfs")
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dest, "readonly"), 0700) })
	require.ErrorIs(t, NewUnpacker(dest).Unpack(archive), io.ErrUnexpectedEOF)
	requireUnpackedFile(t, filepath.Join(dest, "partial"), "short", 0600)
	info, err := os.Stat(filepath.Join(dest, "readonly"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0555), info.Mode().Perm())
}

func TestUnpackerSpecialModes(t *testing.T) {
	testutil.RequireRoot(t)
	for _, tc := range []struct {
		name string
		mode int64
		want os.FileMode
		dir  bool
	}{
		{"sticky directory", 01777, os.ModeSticky | 0777, true},
		{"setuid file", 04755, os.ModeSetuid | 0755, false},
		{"setgid file", 02755, os.ModeSetgid | 0755, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			probe := filepath.Join(base, "probe")
			if tc.dir {
				require.NoError(t, os.Mkdir(probe, 0700))
			} else {
				require.NoError(t, os.WriteFile(probe, nil, 0600))
			}
			require.NoError(t, os.Chmod(probe, tc.want))
			info, err := os.Stat(probe)
			require.NoError(t, err)
			const mask = os.ModePerm | os.ModeSticky | os.ModeSetuid | os.ModeSetgid
			if info.Mode()&mask != tc.want {
				t.Skip("filesystem does not preserve the requested special mode")
			}
			entry := unpackFile("entry", "data", tc.mode)
			if tc.dir {
				entry = unpackDir("entry", tc.mode)
			}
			dest := filepath.Join(base, "rootfs")
			require.NoError(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, entry)))
			info, err = os.Stat(filepath.Join(dest, "entry"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, info.Mode()&mask)
		})
	}
}

func TestUnpackerClosesFilesDuringExtraction(t *testing.T) {
	testutil.RequireRoot(t)
	if os.Getenv("BISTRO_TEST_UNPACK_FD_LIMIT") != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(binary, "-test.run=^TestUnpackerClosesFilesDuringExtraction$")
		cmd.Env = append(os.Environ(), "BISTRO_TEST_UNPACK_FD_LIMIT=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	var original syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original))
	limit := original
	limit.Cur = 64
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit))
	defer syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original)
	var entries []rootFSEntry
	for i := range 128 {
		entries = append(entries, unpackFile(fmt.Sprintf("file%d", i), "data", 0644))
	}
	dest := filepath.Join(t.TempDir(), "rootfs")
	require.NoError(t, NewUnpacker(dest).Unpack(writeUnpackTar(t, entries...)))
	requireUnpackedFile(t, filepath.Join(dest, "file127"), "data", 0644)
}
