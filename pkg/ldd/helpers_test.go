package ldd

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type cacheRecord struct {
	name, path string
	flags      int32
}

func cacheFixture(records ...cacheRecord) []byte {
	// The on-disk header is 48 bytes and each entry is 24 bytes.
	data := make([]byte, 48+24*len(records))
	copy(data, "glibc-ld.so.cache1.1")
	binary.LittleEndian.PutUint32(data[20:], uint32(len(records)))
	data[28] = 2 // little endian
	for i, record := range records {
		offset := 48 + 24*i
		binary.LittleEndian.PutUint32(data[offset:], uint32(record.flags))
		binary.LittleEndian.PutUint32(data[offset+4:], uint32(len(data)))
		data = append(data, []byte(record.name)...)
		data = append(data, 0)
		binary.LittleEndian.PutUint32(data[offset+8:], uint32(len(data)))
		data = append(data, []byte(record.path)...)
		data = append(data, 0)
	}
	binary.LittleEndian.PutUint32(data[24:], uint32(len(data)-48-24*len(records)))
	return data
}

func testResolver(t *testing.T, options ResolverOptions, records ...cacheRecord) *resolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ld.so.cache")
	require.NoError(t, os.WriteFile(path, cacheFixture(records...), 0644))
	r := NewResolver(options).(*resolver)
	require.NoError(t, r.ldSoCache.load(path))
	return r
}

func withSysLibraryPaths(r *resolver, paths ...string) *resolver {
	r.sysLibraryPaths = paths
	return r
}
