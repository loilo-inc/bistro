package ldd

import (
	"debug/elf"
	"fmt"
)

type ABI int32

const (
	kABIArm64_Glibc6  ABI = 0x0a00 | 0x0003
	kABIX86_64_Glibc6 ABI = 0x0300 | 0x0003
)

func cacheFlags(f *elf.File) (ABI, error) {
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB {
		return 0, fmt.Errorf("unsupported ELF class or data encoding")
	}
	switch f.Machine {
	case elf.EM_AARCH64:
		return kABIArm64_Glibc6, nil
	case elf.EM_X86_64:
		return kABIX86_64_Glibc6, nil
	default:
		return 0, fmt.Errorf("unsupported ELF machine: %v", f.Machine)
	}
}
