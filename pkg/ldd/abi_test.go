package ldd

import (
	"debug/elf"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheFlags(t *testing.T) {
	for _, tt := range []struct {
		name    string
		machine elf.Machine
		class   elf.Class
		data    elf.Data
		want    ABI
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
			got, err := cacheFlags(&elf.File{FileHeader: elf.FileHeader{Machine: tt.machine, Class: tt.class, Data: tt.data}})
			if tt.want == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
