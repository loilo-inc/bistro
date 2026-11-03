package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/rootfs"
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
				elftest.WriteDynamic(t, paths[name])
			}
			options := ResolverOptions{}
			if tt.env {
				options.LdLibraryPath = filepath.Dir(paths["env"])
			}
			var entries []elftest.Dyn
			entries = append(entries, elftest.Needed("lib.so")...)
			if tt.rpath {
				entries = append(entries, elftest.DynString(elf.DT_RPATH, filepath.Dir(paths["rpath"])))
			}
			if tt.runpath {
				entries = append(entries, elftest.DynString(elf.DT_RUNPATH, filepath.Dir(paths["runpath"])))
			}
			var records []cacheRecord
			if tt.cache {
				records = append(records, cacheRecord{"lib.so", paths["cache"], 0x303})
			}
			root := filepath.Join(dir, "app")
			elftest.WriteDynamic(t, root, entries...)
			libs, err := withSysLibraryPaths(testResolver(t, options, records...), filepath.Dir(paths["system"])).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Equal(t, paths[tt.want], libs[0].Path.Abs())
		})
	}
}

func TestResolverBrokenAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	elftest.WriteDynamic(t, "libs/real.so")
	require.NoError(t, os.Symlink("real.so", "libs/link.so"))
	require.NoError(t, os.Mkdir("libs/directory.so", 0755))
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, append(elftest.Needed("link.so", "directory.so", "missing.so", "./absent.so", filepath.Join(dir, "absent.so")), elftest.DynString(elf.DT_RUNPATH, "libs"))...)
	r := testResolver(t, ResolverOptions{})
	r.fs = os.DirFS(dir)
	libs, err := r.ListObjects("/app")
	require.NoError(t, err)
	require.Len(t, libs, 5)
	require.Equal(t, "/libs/link.so", libs[0].Path.Abs())
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
			elftest.Write(t, path, elftest.Fixture{Machine: elf.EM_AARCH64})
		}},
		{"unreadable", func(t *testing.T, path string) {
			if os.Geteuid() == 0 {
				t.Skip("root ignores file permissions")
			}
			elftest.WriteDynamic(t, path)
			require.NoError(t, os.Chmod(path, 0))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bad, good := filepath.Join(dir, "bad"), filepath.Join(dir, "good")
			tt.write(t, filepath.Join(bad, "lib.so"))
			elftest.WriteDynamic(t, filepath.Join(good, "lib.so"))
			root := filepath.Join(dir, "app")
			elftest.WriteDynamic(t, root, elftest.DynString(elf.DT_NEEDED, "lib.so"), elftest.DynString(elf.DT_RUNPATH, bad+":"+good))
			libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Equal(t, filepath.Join(good, "lib.so"), libs[0].Path.Abs())
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
			elftest.WriteNotELF(t, path)
		}, "malformed ELF file"},
		{"relocatable object", func(t *testing.T, path string) {
			elftest.Write(t, path, elftest.Fixture{Type: elf.ET_REL})
		}, "invalid ELF type"},
		{"foreign OS ABI", func(t *testing.T, path string) {
			elftest.Write(t, path, elftest.Fixture{Header: func(h *elf.Header64) { h.Ident[elf.EI_OSABI] = byte(elf.ELFOSABI_FREEBSD) }})
		}, "incompatible ELF headers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bad, good := filepath.Join(dir, "bad"), filepath.Join(dir, "good")
			badLib := filepath.Join(bad, "lib.so")
			tt.write(t, badLib)
			elftest.WriteDynamic(t, filepath.Join(good, "lib.so"))
			root := filepath.Join(dir, "app")
			elftest.WriteDynamic(t, root, elftest.DynString(elf.DT_NEEDED, "lib.so"), elftest.DynString(elf.DT_RUNPATH, bad+":"+good))
			_, err := testResolver(t, ResolverOptions{}).ListObjects(root)
			require.ErrorContains(t, err, tt.want)
			require.ErrorContains(t, err, rootfs.NewPath(badLib).Path(), "the error must name the malformed candidate")
		})
	}
}

func TestPathResolverNoCompatibleCandidate(t *testing.T) {
	dir := t.TempDir()
	elftest.Write(t, filepath.Join(dir, "other.so"), elftest.Fixture{Machine: elf.EM_AARCH64})
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, append(elftest.Needed("other.so", filepath.Join(dir, "other.so"), "missing.so"), elftest.DynString(elf.DT_RUNPATH, dir))...)
	libs, err := testResolver(t, ResolverOptions{}).ListObjects(root)
	require.NoError(t, err)
	require.Len(t, libs, 3)
	for _, lib := range libs {
		require.True(t, lib.IsBroken(), lib.Name)
	}
}

func TestPathResolverVerifiesCacheEntry(t *testing.T) {
	for _, tt := range []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"stale entry", func(*testing.T, string) {}},
		{"other machine", func(t *testing.T, path string) {
			elftest.Write(t, path, elftest.Fixture{Machine: elf.EM_AARCH64})
		}},
		{"directory", func(t *testing.T, path string) {
			require.NoError(t, os.MkdirAll(path, 0755))
		}},
		{"symlink to directory", func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			require.NoError(t, os.MkdirAll(target, 0755))
			require.NoError(t, os.Symlink(target, path))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cached, system := filepath.Join(dir, "cache", "lib.so"), filepath.Join(dir, "system", "lib.so")
			tt.write(t, cached)
			elftest.WriteDynamic(t, system)
			root := filepath.Join(dir, "app")
			elftest.WriteDynamic(t, root, elftest.Needed("lib.so")...)
			r := withSysLibraryPaths(testResolver(t, ResolverOptions{}, cacheRecord{"lib.so", cached, 0x303}), filepath.Dir(system))
			libs, err := r.ListObjects(root)
			require.NoError(t, err)
			require.Len(t, libs, 1)
			require.Equal(t, system, libs[0].Path.Abs(), "fall back to system paths when the cache entry is unusable")
		})
	}
}

func TestPathResolverAbortsOnMalformedCacheEntry(t *testing.T) {
	dir := t.TempDir()
	cached := filepath.Join(dir, "lib.so")
	elftest.WriteNotELF(t, cached)
	root := filepath.Join(dir, "app")
	elftest.WriteDynamic(t, root, elftest.Needed("lib.so")...)
	_, err := testResolver(t, ResolverOptions{}, cacheRecord{"lib.so", cached, 0x303}).ListObjects(root)
	require.ErrorContains(t, err, "[ldd/readElf] malformed ELF file: "+rootfs.NewPath(cached).Path()+": ")
	var formatErr *elf.FormatError
	require.ErrorAs(t, err, &formatErr)
}
