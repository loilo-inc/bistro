package ldd

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/testfs"
	"github.com/stretchr/testify/require"
)

func TestCacheResolveLibrary(t *testing.T) {
	cache := testResolver(t, ResolverOptions{},
		cacheRecord{"libfoo.so", "/arm/libfoo.so", 0xa03},
		cacheRecord{"libfoo.so", "/amd/libfoo.so", 0x303},
		cacheRecord{"libfoo.so", "/other/libfoo.so", 0x303},
		cacheRecord{"libfoo.so", "/i386/libfoo.so", 3},
		cacheRecord{"arm-only.so", "/arm/only.so", 0xa03},
		cacheRecord{"wrong-type.so", "/wrong/type.so", 0x301},
	).ldSoCache
	for _, tt := range []struct {
		name string
		abi  cacheABI
		want string
	}{
		{"libfoo.so", kABIFlagArm64_Glibc6, "/arm/libfoo.so"},
		{"libfoo.so", kABIFlagX86_64_Glibc6, "/amd/libfoo.so"},
		{"arm-only.so", kABIFlagX86_64_Glibc6, ""},
		{"wrong-type.so", kABIFlagX86_64_Glibc6, ""},
		{"missing.so", kABIFlagX86_64_Glibc6, ""},
	} {
		// Repeat requests to cover both hits and misses after memoization.
		for range 2 {
			path, ok := cache.ResolveLibrary(tt.name, tt.abi)
			require.Equal(t, tt.want, path)
			require.Equal(t, tt.want != "", ok)
		}
	}
}

func TestParseLdSoCacheInvalid(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func([]byte) []byte
		want   string
	}{
		{"truncated header", func(b []byte) []byte { return b[:20] }, "EOF"},
		{"magic", func(b []byte) []byte { b[0] = '?'; return b }, "unsupported cache format"},
		{"version", func(b []byte) []byte { b[19] = '2'; return b }, "unsupported cache format"},
		{"byte order", func(b []byte) []byte { b[28] = 3; return b }, "unsupported byte order"},
		{"truncated entry", func(b []byte) []byte { return b[:60] }, "EOF"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLdSoCache(tt.mutate(cacheFixture(cacheRecord{"lib.so", "/lib/lib.so", 0x303})))
			require.ErrorContains(t, err, tt.want)
		})
	}
	entries, err := ParseLdSoCache(cacheFixture())
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCacheLoadFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ld.so.cache")
	cache := NewLdCache(testfs.Mount(os.DirFS("/"), "etc", os.DirFS(dir))).(*ldCache)
	require.NoError(t, cache.Load())
	resolved, ok := cache.ResolveLibrary("missing.so", kABIFlagX86_64_Glibc6)
	require.Empty(t, resolved)
	require.False(t, ok)
	require.NoError(t, os.WriteFile(path, []byte("invalid"), 0644))
	require.ErrorIs(t, cache.Load(), io.ErrUnexpectedEOF)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0755))
	err := cache.Load()
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, path, pathErr.Path)
	require.ErrorIs(t, err, syscall.EISDIR)
}

func TestParseLdSoCache(t *testing.T) {
	data := cacheFixture(
		cacheRecord{"libfoo.so", "/arm/libfoo.so", 0xa03},
		cacheRecord{"libbar.so", "/amd/libbar.so", 0x303},
	)
	entries, err := ParseLdSoCache(data)
	require.NoError(t, err)
	require.Equal(t, []*CacheEntry{
		{Flags: 0xa03, Key: 96, Value: 106},
		{Flags: 0x303, Key: 121, Value: 131},
	}, entries)
}

func TestCacheReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ld.so.cache")
	cache := NewLdCache(testfs.Mount(os.DirFS("/"), "etc", os.DirFS(dir))).(*ldCache)
	for _, libPath := range []string{"/first/libfoo.so", "/second/libfoo.so"} {
		require.NoError(t, os.WriteFile(path, cacheFixture(cacheRecord{"libfoo.so", libPath, 0x303}), 0644))
		require.NoError(t, cache.Load())
		resolved, ok := cache.ResolveLibrary("libfoo.so", kABIFlagX86_64_Glibc6)
		require.True(t, ok)
		require.Equal(t, libPath, resolved)
		require.Len(t, cache.keyMap.Get("libfoo.so"), 1)
	}
}

func TestCacheSkipsHWCapEntries(t *testing.T) {
	data := cacheFixture(
		cacheRecord{"libfoo.so", "/lib/glibc-hwcaps/x86-64-v3/libfoo.so", 0x303},
		cacheRecord{"libfoo.so", "/lib/libfoo.so", 0x303},
	)
	// HWCap of the first entry: the glibc-hwcaps extension bit and subdirectory index 0.
	binary.LittleEndian.PutUint64(data[48+16:], 1<<62)
	path := filepath.Join(t.TempDir(), "ld.so.cache")
	require.NoError(t, os.WriteFile(path, data, 0644))
	cache := NewLdCache(testfs.Mount(os.DirFS("/"), "etc", os.DirFS(filepath.Dir(path)))).(*ldCache)
	require.NoError(t, cache.Load())
	resolved, ok := cache.ResolveLibrary("libfoo.so", kABIFlagX86_64_Glibc6)
	require.True(t, ok)
	require.Equal(t, "/lib/libfoo.so", resolved)
}
