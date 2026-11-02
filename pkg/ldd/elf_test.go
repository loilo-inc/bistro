package ldd

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandVars(t *testing.T) {
	for _, tt := range []struct {
		name, path, want string
	}{
		{"empty", "", ""},
		{"literal list", "/lib:/usr/lib", "/lib:/usr/lib"},
		{"tokens", "$ORIGIN/$LIB:${ORIGIN}/${PLATFORM}", "/app/lib/aarch64-linux-gnu:/app/aarch64"},
		{"braced LIB", "${ORIGIN}/${LIB}", "/app/lib/aarch64-linux-gnu"},
		{"unbraced PLATFORM", "$PLATFORM/lib", "aarch64/lib"},
		{"unknown token", "$UNKNOWN/lib", "$UNKNOWN/lib"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := expandVars(tt.path, "/app", "lib/aarch64-linux-gnu", "aarch64")
			require.Equal(t, tt.want, got)
		})
	}
	require.Equal(t, "/app/$build/lib", expandVars("$ORIGIN/lib", "/app/$build", "lib", "aarch64"))
}

func TestReadElf(t *testing.T) {
	for _, runpath := range []string{"$ORIGIN/run", ""} {
		t.Run("RUNPATH="+runpath, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "libfoo.so")
			entries := append(needed("libone.so", "libtwo.so"), dynamicString{elf.DT_SONAME, "libfoo.so"}, dynamicString{elf.DT_RPATH, "$PLATFORM/ignored"}, dynamicString{elf.DT_RUNPATH, runpath})
			writeELF(t, path, entries...)
			info, err := readElf(path, "lib", "")
			require.NoError(t, err)
			require.Equal(t, path, info.Path)
			require.Equal(t, kABIX86_64_Glibc6, info.ABI)
			require.Equal(t, "libfoo.so", info.SONAME)
			require.Equal(t, []string{"libone.so", "libtwo.so"}, info.Needed)
			require.True(t, info.HasRunPath)
			require.Empty(t, info.RPaths, "RUNPATH suppresses RPATH even when the tag is empty")
			if runpath == "" {
				require.Empty(t, info.RunPaths)
			} else {
				require.Equal(t, []string{filepath.Join(filepath.Dir(path), "run")}, info.RunPaths)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "plain.so")
	writeELF(t, path, dynamicString{elf.DT_RPATH, "$ORIGIN"})
	info, err := readElf(path, "lib", "")
	require.NoError(t, err)
	require.Empty(t, info.SONAME)
	require.Empty(t, info.Needed)
	require.False(t, info.HasRunPath)
	require.Equal(t, []string{filepath.Dir(path)}, info.RPaths)
}

func TestReadElfCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.so")
	writeELF(t, path)
	r := &rootResolver{resolver: NewResolver(ResolverOptions{})}
	first, err := r.readElf(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	second, err := r.readElf(path)
	require.NoError(t, err)
	require.Same(t, first, second, "cached metadata must not reopen the file")
}
