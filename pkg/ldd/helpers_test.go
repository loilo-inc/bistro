package ldd

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type dynamicString struct {
	tag   elf.DynTag
	value string
}

func needed(names ...string) []dynamicString {
	var entries []dynamicString
	for _, name := range names {
		entries = append(entries, dynamicString{elf.DT_NEEDED, name})
	}
	return entries
}

// These fixtures are parsed, never executed. No compiler or host Linux files
// are needed, and the dynamic string table preserves empty RUNPATH tags.
func writeELF(t *testing.T, path string, entries ...dynamicString) {
	t.Helper()
	strings := []byte{0}
	var dynamic bytes.Buffer
	for _, entry := range entries {
		require.NoError(t, binary.Write(&dynamic, binary.LittleEndian, elf.Dyn64{Tag: int64(entry.tag), Val: uint64(len(strings))}))
		strings = append(strings, []byte(entry.value)...)
		strings = append(strings, 0)
	}
	require.NoError(t, binary.Write(&dynamic, binary.LittleEndian, elf.Dyn64{}))
	header := elf.Header64{Type: uint16(elf.ET_DYN), Machine: uint16(elf.EM_X86_64), Version: 1, Ehsize: 64, Shoff: uint64(64 + len(strings) + dynamic.Len()), Shentsize: 64, Shnum: 3}
	copy(header.Ident[:], []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	var out bytes.Buffer
	require.NoError(t, binary.Write(&out, binary.LittleEndian, header))
	out.Write(strings)
	out.Write(dynamic.Bytes())
	for _, section := range []elf.Section64{
		{},
		{Type: uint32(elf.SHT_STRTAB), Off: 64, Size: uint64(len(strings))},
		{Type: uint32(elf.SHT_DYNAMIC), Off: uint64(64 + len(strings)), Size: uint64(dynamic.Len()), Link: 1, Entsize: 16},
	} {
		require.NoError(t, binary.Write(&out, binary.LittleEndian, section))
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, out.Bytes(), 0644))
}

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
	r := NewResolver(options)
	require.NoError(t, r.ldSoCache.load(path))
	return r
}
