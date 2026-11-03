package ldd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type CacheHeader struct {
	Magic           [17]byte
	Version         [3]byte
	NLibs           uint32
	LenStrings      uint32
	Flags           uint8
	Padding         [3]byte
	ExtensionOffset uint32
	Unused          [3]uint32
}

type CacheEntry struct {
	Flags           int32
	Key             uint32
	Value           uint32
	OSVersionUnused uint32
	HWCap           uint64
}

type LdCache interface {
	ResolveLibrary(libName string, abiFlag cacheABI) (string, bool)
	Load() error
}

func NewLdCache(fs fs.FS) LdCache {
	return &ldCache{fs: fs}
}

var _ LdCache = (*ldCache)(nil)

type ldCache struct {
	entries []*CacheEntry
	data    []byte
	keyMap  util.Map[string, []*CacheEntry]
	fs      fs.FS
}

func (l *ldCache) ResolveLibrary(libName string, abiFlag cacheABI) (string, bool) {
	path := l.doResolve(libName, abiFlag)
	return path, path != ""
}

func (l *ldCache) doResolve(libName string, abiFlag cacheABI) string {
	candidates, ok := l.keyMap.LookUp(libName)
	if !ok {
		return ""
	}
	for _, entry := range candidates {
		// Use only baseline entries; glibc-hwcaps variants depend on the deployment host's CPU.
		if entry.Flags == int32(abiFlag) && entry.HWCap == 0 {
			return l.readString(entry.Value)
		}
	}
	return ""
}

func (l *ldCache) readString(offset uint32) string {
	if uint64(offset) >= uint64(len(l.data)) {
		return ""
	}
	b := l.data[offset:]
	if before, _, ok := bytes.Cut(b, []byte{0}); ok {
		return string(before)
	}
	return ""
}

func (l *ldCache) Load() error {
	return l.load(rootfs.NewPath(DefaultCachePath))
}

func (l *ldCache) load(path rootfs.Path) error {
	data, err := fs.ReadFile(l.fs, path.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	entries, err := ParseLdSoCache(data)
	if err != nil {
		return err
	}
	l.data = data
	l.entries = entries
	l.keyMap.Clear()
	for _, entry := range entries {
		k := l.readString(entry.Key)
		l.keyMap.Set(k, append(l.keyMap.Get(k), entry))
	}
	return nil
}

const DefaultCachePath = "/etc/ld.so.cache"

func ParseLdSoCache(data []byte) ([]*CacheEntry, error) {
	r := bytes.NewReader(data)
	var header CacheHeader
	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		return nil, err
	}

	if string(header.Magic[:]) != "glibc-ld.so.cache" ||
		string(header.Version[:]) != "1.1" {
		return nil, fmt.Errorf("unsupported cache format")
	}

	if header.Flags&3 != 2 {
		return nil, fmt.Errorf("unsupported byte order")
	}

	entries := make([]*CacheEntry, 0)

	for i := uint32(0); i < header.NLibs; i++ {
		var entry CacheEntry
		if err := binary.Read(r, binary.LittleEndian, &entry); err != nil {
			return nil, err
		}
		entries = append(entries, &entry)
	}

	return entries, nil
}
