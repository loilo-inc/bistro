package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolverSearchOrder(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		rpath, runpath, env, cache bool
		want                       string
	}{
		{"RPATH before environment", true, false, true, true, "rpath"},
		{"RUNPATH disables RPATH", true, true, true, true, "env"},
		{"RUNPATH before cache", true, true, false, true, "runpath"},
		{"cache before system", false, false, false, true, "cache"},
		{"system fallback", false, false, false, false, "system"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := make(map[string]string)
			for _, name := range []string{"rpath", "env", "runpath", "cache", "system"} {
				paths[name] = filepath.Join(dir, name, "lib.so")
				writeELF(t, paths[name])
			}
			options := ResolverOptions{}
			if tt.env {
				options.LdLibraryPath = filepath.Dir(paths["env"])
			}
			var entries []dynamicString
			entries = append(entries, needed("lib.so")...)
			if tt.rpath {
				entries = append(entries, dynamicString{elf.DT_RPATH, filepath.Dir(paths["rpath"])})
			}
			if tt.runpath {
				entries = append(entries, dynamicString{elf.DT_RUNPATH, filepath.Dir(paths["runpath"])})
			}
			var records []cacheRecord
			if tt.cache {
				records = append(records, cacheRecord{"lib.so", paths["cache"], 0x303})
			}
			root := filepath.Join(dir, "app")
			writeELF(t, root, entries...)
			libs, err := withSysLibraryPaths(testResolver(t, options, records...), filepath.Dir(paths["system"])).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Equal(t, paths[tt.want], libs[0].Path)
		})
	}
}

func TestResolverBrokenAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeELF(t, "libs/real.so")
	require.NoError(t, os.Symlink("real.so", "libs/link.so"))
	require.NoError(t, os.Mkdir("libs/directory.so", 0755))
	root := filepath.Join(dir, "app")
	writeELF(t, root, append(needed("link.so", "directory.so", "missing.so", "./absent.so", filepath.Join(dir, "absent.so")), dynamicString{elf.DT_RUNPATH, "libs"})...)
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 5)
	abs, err := filepath.Abs("libs/link.so")
	require.NoError(t, err)
	require.Equal(t, abs, libs[0].Path)
	for _, lib := range libs[1:] {
		require.True(t, lib.IsBroken(), lib.Name)
		require.Empty(t, lib.Deps)
	}
}

func TestPathResolverSkipsIncompatibleCandidates(t *testing.T) {
	for _, tt := range []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"directory", func(t *testing.T, path string) {
			require.NoError(t, os.MkdirAll(path, 0755))
		}},
		{"other machine", func(t *testing.T, path string) {
			writeELFWithHeader(t, path, func(h *elf.Header64) { h.Machine = uint16(elf.EM_AARCH64) })
		}},
		{"unreadable", func(t *testing.T, path string) {
			if os.Geteuid() == 0 {
				t.Skip("root ignores file permissions")
			}
			writeELF(t, path)
			require.NoError(t, os.Chmod(path, 0))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bad, good := filepath.Join(dir, "bad"), filepath.Join(dir, "good")
			tt.write(t, filepath.Join(bad, "lib.so"))
			writeELF(t, filepath.Join(good, "lib.so"))
			root := filepath.Join(dir, "app")
			writeELF(t, root, dynamicString{elf.DT_NEEDED, "lib.so"}, dynamicString{elf.DT_RUNPATH, bad + ":" + good})
			libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Equal(t, filepath.Join(good, "lib.so"), libs[0].Path)
		})
	}
}

func TestPathResolverAbortsOnMalformedCandidate(t *testing.T) {
	for _, tt := range []struct {
		name  string
		write func(t *testing.T, path string)
		want  string
	}{
		{"not ELF", func(t *testing.T, path string) {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte("not ELF"), 0644))
		}, "malformed ELF file"},
		{"relocatable object", func(t *testing.T, path string) {
			writeELFWithHeader(t, path, func(h *elf.Header64) { h.Type = uint16(elf.ET_REL) })
		}, "invalid ELF type"},
		{"foreign OS ABI", func(t *testing.T, path string) {
			writeELFWithHeader(t, path, func(h *elf.Header64) { h.Ident[elf.EI_OSABI] = byte(elf.ELFOSABI_FREEBSD) })
		}, "incompatible ELF headers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bad, good := filepath.Join(dir, "bad"), filepath.Join(dir, "good")
			badLib := filepath.Join(bad, "lib.so")
			tt.write(t, badLib)
			writeELF(t, filepath.Join(good, "lib.so"))
			root := filepath.Join(dir, "app")
			writeELF(t, root, dynamicString{elf.DT_NEEDED, "lib.so"}, dynamicString{elf.DT_RUNPATH, bad + ":" + good})
			_, err := testResolver(t, ResolverOptions{}).ListObjects(root)
			require.ErrorContains(t, err, tt.want)
			require.ErrorContains(t, err, badLib, "the error must name the malformed candidate")
		})
	}
}

func TestPathResolverNoCompatibleCandidate(t *testing.T) {
	dir := t.TempDir()
	writeELFWithHeader(t, filepath.Join(dir, "other.so"), func(h *elf.Header64) { h.Machine = uint16(elf.EM_AARCH64) })
	root := filepath.Join(dir, "app")
	writeELF(t, root, append(needed("other.so", filepath.Join(dir, "other.so"), "missing.so"), dynamicString{elf.DT_RUNPATH, dir})...)
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 3)
	for _, lib := range libs {
		require.True(t, lib.IsBroken(), lib.Name)
	}
}
