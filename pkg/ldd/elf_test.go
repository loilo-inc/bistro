package ldd

import (
	"debug/elf"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/elftest"
	"github.com/loilo-inc/bistro/pkg/rootfs"
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
			entries := append(elftest.Needed("libone.so", "libtwo.so"), elftest.DynString(elf.DT_SONAME, "libfoo.so"), elftest.DynString(elf.DT_RPATH, "$PLATFORM/ignored"), elftest.DynString(elf.DT_RUNPATH, runpath))
			elftest.WriteDynamic(t, path, entries...)
			info, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
			require.NoError(t, err)
			require.Equal(t, path, info.Path.Abs())
			require.Equal(t, kABIFlagX86_64_Glibc6, info.ldCacheABI())
			require.Equal(t, "libfoo.so", info.SONAME)
			require.Equal(t, []string{"libone.so", "libtwo.so"}, info.Needed)
			require.True(t, info.HasRunPath)
			require.Empty(t, info.RPaths, "RUNPATH suppresses RPATH even when the tag is empty")
			if runpath == "" {
				require.Empty(t, info.RunPaths)
			} else {
				require.Equal(t, rootfs.MustPaths(filepath.Join(filepath.Dir(path), "run")), info.RunPaths)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "plain.so")
	elftest.WriteDynamic(t, path, elftest.DynString(elf.DT_RPATH, "$ORIGIN"))
	info, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Empty(t, info.SONAME)
	require.Empty(t, info.Needed)
	require.False(t, info.HasRunPath)
	require.Equal(t, rootfs.MustPaths(filepath.Dir(path)), info.RPaths)
}

func TestCacheFlags(t *testing.T) {
	for _, tt := range []struct {
		name    string
		machine elf.Machine
		class   elf.Class
		data    elf.Data
		want    cacheABI
	}{
		{"amd64", elf.EM_X86_64, elf.ELFCLASS64, elf.ELFDATA2LSB, 0x303},
		{"arm64", elf.EM_AARCH64, elf.ELFCLASS64, elf.ELFDATA2LSB, 0xa03},
		{"x32", elf.EM_X86_64, elf.ELFCLASS32, elf.ELFDATA2LSB, 0},
		{"i386", elf.EM_386, elf.ELFCLASS32, elf.ELFDATA2LSB, 0},
		{"arm64 big endian", elf.EM_AARCH64, elf.ELFCLASS64, elf.ELFDATA2MSB, 0},
		{"riscv64", elf.EM_RISCV, elf.ELFCLASS64, elf.ELFDATA2LSB, 0},
		{"unknown class", elf.EM_X86_64, elf.ELFCLASSNONE, elf.ELFDATA2LSB, 0},
		{"unknown encoding", elf.EM_X86_64, elf.ELFCLASS64, elf.ELFDATANONE, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := (&elfInfo{FileHeader: elf.FileHeader{Machine: tt.machine, Class: tt.class, Data: tt.data}}).ldCacheABI()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestElfInfoVerify(t *testing.T) {
	src := &elfInfo{FileHeader: elf.FileHeader{
		Class: elf.ELFCLASS64, Data: elf.ELFDATA2LSB, Version: elf.EV_CURRENT,
		OSABI: elf.ELFOSABI_NONE, Type: elf.ET_EXEC, Machine: elf.EM_X86_64,
	}}
	for _, tt := range []struct {
		name    string
		mutate  func(*elf.FileHeader)
		ok      bool
		wantErr string
	}{
		{"compatible", func(*elf.FileHeader) {}, true, ""},
		{"executable", func(h *elf.FileHeader) { h.Type = elf.ET_EXEC }, true, ""},
		{"GNU OS ABI", func(h *elf.FileHeader) { h.OSABI = elf.ELFOSABI_LINUX }, true, ""},
		{"other class", func(h *elf.FileHeader) { h.Class = elf.ELFCLASS32 }, false, ""},
		{"other machine", func(h *elf.FileHeader) { h.Machine = elf.EM_AARCH64 }, false, ""},
		{"other machine wins over bad type", func(h *elf.FileHeader) { h.Machine = elf.EM_AARCH64; h.Type = elf.ET_REL }, false, ""},
		{"relocatable", func(h *elf.FileHeader) { h.Type = elf.ET_REL }, false, "invalid ELF type"},
		{"core", func(h *elf.FileHeader) { h.Type = elf.ET_CORE }, false, "invalid ELF type"},
		{"big endian", func(h *elf.FileHeader) { h.Data = elf.ELFDATA2MSB }, false, "incompatible ELF headers"},
		{"other version", func(h *elf.FileHeader) { h.Version = elf.EV_NONE }, false, "incompatible ELF headers"},
		{"foreign OS ABI", func(h *elf.FileHeader) { h.OSABI = elf.ELFOSABI_FREEBSD }, false, "incompatible ELF headers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			header := src.FileHeader
			header.Type = elf.ET_DYN
			tt.mutate(&header)
			ok, err := src.verify(&elfInfo{FileHeader: header, Path: rootfs.MustPath("/lib/target.so")})
			require.Equal(t, tt.ok, ok)
			if tt.wantErr != "" {
				require.EqualError(t, err, "[ldd/verify] "+tt.wantErr+" for target: /lib/target.so")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReadElfWithoutSectionHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libfoo.so")
	noSections := func(h *elf.Header64) { h.Shoff, h.Shnum, h.Shentsize = 0, 0, 0 }
	elftest.Write(t, path, elftest.Fixture{Header: noSections, Entries: []elftest.Dyn{
		elftest.DynString(elf.DT_NEEDED, "libone.so"),
		elftest.DynString(elf.DT_SONAME, "libfoo.so"),
		elftest.DynString(elf.DT_RPATH, "/rpath"),
		elftest.DynString(elf.DT_NEEDED, "libtwo.so"),
	}})
	info, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Equal(t, []string{"libone.so", "libtwo.so"}, info.Needed)
	require.Equal(t, "libfoo.so", info.SONAME)
	require.Equal(t, rootfs.MustPaths("/rpath"), info.RPaths)

	elftest.Write(t, path, elftest.Fixture{Header: noSections, Entries: []elftest.Dyn{
		elftest.DynString(elf.DT_RUNPATH, "/runpath"),
	}})
	info, err = (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.True(t, info.HasRunPath)
	require.Equal(t, rootfs.MustPaths("/runpath"), info.RunPaths)
}

func TestReadElfIgnoresSectionHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libfoo.so")
	elftest.Write(t, path, elftest.Fixture{
		Entries: elftest.Needed("libone.so"),
		// Shrink SHT_DYNAMIC below one entry; section-based readers fail on it.
		Sections: func(s []elf.Section64) { s[2].Size = 1 },
	})
	info, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Equal(t, []string{"libone.so"}, info.Needed)
}

func TestReadElfWithoutDynamicSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "static")
	// Section headers still describe DT_NEEDED; only program headers count.
	elftest.Write(t, path, elftest.Fixture{
		Header:  func(h *elf.Header64) { h.Phoff, h.Phnum = 0, 0 },
		Entries: elftest.Needed("libone.so"),
	})
	info, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Empty(t, info.Needed, "a file without PT_DYNAMIC has no dynamic dependencies")
	require.Empty(t, info.SONAME)
}

func TestReadElfMalformedDynamicSegment(t *testing.T) {
	// Strings start right after the ELF header and two program headers.
	const strtab = 64 + 2*56
	for _, tt := range []struct {
		name string
		fx   elftest.Fixture
		want string
	}{
		// Section headers follow the dynamic table, so extend well past the end of file.
		{"truncated PT_DYNAMIC", elftest.Fixture{Progs: func(p []elf.Prog64) { elftest.Prog(p, elf.PT_DYNAMIC).Filesz += 1 << 20 }},
			"[ldd/readDynTable] truncated PT_DYNAMIC"},
		{"string table outside PT_LOAD", elftest.Fixture{Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRTAB), Val: 1 << 40}}},
			"[ldd/readVaddr] vaddr 0x10000000000 not in any PT_LOAD"},
		{"string table larger than PT_LOAD", elftest.Fixture{Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRSZ), Val: 1 << 62}}},
			fmt.Sprintf("[ldd/readVaddr] vaddr 0x%x not in any PT_LOAD", strtab)},
		{"overflowing string table bounds", elftest.Fixture{Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRSZ), Val: math.MaxUint64 - strtab + 1}}},
			fmt.Sprintf("[ldd/readVaddr] vaddr 0x%x not in any PT_LOAD", strtab)},
		{"PT_LOAD past end of file", elftest.Fixture{
			Progs: func(p []elf.Prog64) { elftest.Prog(p, elf.PT_LOAD).Filesz += 1 << 20 },
			Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRSZ), Val: 1 << 19}},
		}, fmt.Sprintf("[ldd/readVaddr] truncated data at vaddr 0x%x", strtab)},
		{"string offset out of range", elftest.Fixture{Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRSZ), Val: 1}}},
			"[ldd/readDynTable] string offset out of range: 1"},
		{"missing DT_STRTAB", elftest.Fixture{Omit: []elf.DynTag{elf.DT_STRTAB}},
			"[ldd/readDynTable] DT_STRTAB is missing"},
		{"missing DT_STRSZ", elftest.Fixture{Omit: []elf.DynTag{elf.DT_STRSZ}},
			"[ldd/readDynTable] DT_STRSZ is missing"},
		{"one-byte PT_DYNAMIC", elftest.Fixture{Progs: func(p []elf.Prog64) { elftest.Prog(p, elf.PT_DYNAMIC).Filesz = 1 }},
			"[ldd/readDynTable] truncated dynamic entry"},
		// Cutting DT_NULL in half leaves a partial entry before the terminator.
		{"partial DT_NULL", elftest.Fixture{Progs: func(p []elf.Prog64) { elftest.Prog(p, elf.PT_DYNAMIC).Filesz -= 8 }},
			"[ldd/readDynTable] truncated dynamic entry"},
		// The string table is "\x00libone.so\x00"; nine bytes cut the NUL after "libone.s".
		{"unterminated string", elftest.Fixture{Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRSZ), Val: 9}}},
			"[ldd/readDynTable] unterminated string at offset 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lib.so")
			tt.fx.Entries = elftest.Needed("libone.so")
			elftest.Write(t, path, tt.fx)
			_, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
			require.EqualError(t, err, "[ldd/readElf] malformed ELF file: "+path+": "+tt.want)
		})
	}
}

func TestReadElfWithoutStringEntries(t *testing.T) {
	// ld.so itself has PT_DYNAMIC but no DT_NEEDED; the string table is not needed then.
	path := filepath.Join(t.TempDir(), "ld.so")
	elftest.Write(t, path, elftest.Fixture{Omit: []elf.DynTag{elf.DT_STRTAB, elf.DT_STRSZ}})
	info, err := (&rootResolver{resolver: &resolver{fs: os.DirFS("/"), dstLib: "lib"}}).readElf(rootfs.MustPath(path))
	require.NoError(t, err)
	require.Empty(t, info.Needed)
	require.Empty(t, info.SONAME)
	require.False(t, info.HasRunPath)
}
