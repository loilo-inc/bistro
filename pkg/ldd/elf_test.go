package ldd

import (
	"debug/elf"
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
			require.Equal(t, kABIFlagX86_64_Glibc6, info.ldCacheABI())
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
			ok, err := src.verify(&elfInfo{FileHeader: header, Path: "/lib/target.so"})
			require.Equal(t, tt.ok, ok)
			if tt.wantErr != "" {
				require.EqualError(t, err, "[ldd/verify] "+tt.wantErr+" for target: /lib/target.so")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
