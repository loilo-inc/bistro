package ldd

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type elfInfo struct {
	elf.FileHeader
	*elfPath
	Path   rootfs.Path
	SONAME string
	Needed []string
}

func newElf(file *elf.File, path rootfs.Path, dstLib string, dstPlatform string) (*elfInfo, error) {

	dynTable, err := readDynTable(file)
	if err != nil {
		return nil, err
	}

	dtNeeded := dynTable[elf.DT_NEEDED]
	dtSoname := dynTable[elf.DT_SONAME]

	var soname string
	if len(dtSoname) > 0 {
		soname = dtSoname[0]
	}

	elfPath, err := expandElfPaths(path, dynTable, dstLib, dstPlatform)
	if err != nil {
		return nil, err
	}
	return &elfInfo{
		elfPath:    elfPath,
		Path:       path,
		SONAME:     soname,
		Needed:     dtNeeded,
		FileHeader: file.FileHeader,
	}, nil
}

type elfPath struct {
	Origin     string
	RPaths     []rootfs.Path
	RunPaths   []rootfs.Path
	HasRunPath bool
}

func expandElfPaths(path rootfs.Path, dynTable map[elf.DynTag][]string, dstLib string, dstPlatform string) (*elfPath, error) {
	var rPaths []rootfs.Path
	var runPaths []rootfs.Path

	dtRPath := dynTable[elf.DT_RPATH]
	dtRunPath := dynTable[elf.DT_RUNPATH]

	origin := filepath.Dir(path.Path())
	hasRunPath := len(dtRunPath) > 0

	// only evalueate DT_RPATH if DT_RUNPATH is not present
	if !hasRunPath {
		for _, p := range dtRPath {
			exp := expandVars(p, origin, dstLib, dstPlatform)
			paths := filepath.SplitList(exp)
			for _, p := range paths {
				rPaths = append(rPaths, rootfs.NewPath(p))
			}
		}
	}

	for _, p := range dtRunPath {
		exp := expandVars(p, origin, dstLib, dstPlatform)
		paths := filepath.SplitList(exp)
		for _, p := range paths {
			runPaths = append(runPaths, rootfs.NewPath(p))
		}
	}

	return &elfPath{
		Origin:     origin,
		RPaths:     rPaths,
		RunPaths:   runPaths,
		HasRunPath: hasRunPath,
	}, nil
}

var originPat = regexp.MustCompile(`\$\{?ORIGIN\}?`)
var libPat = regexp.MustCompile(`\$\{?LIB\}?`)
var platformPat = regexp.MustCompile(`\$\{?PLATFORM\}?`)

func expandVars(path string, dstOrigin string, dstLib string, dstPlatform string) string {
	path = originPat.ReplaceAllLiteralString(path, dstOrigin)
	path = libPat.ReplaceAllLiteralString(path, dstLib)
	path = platformPat.ReplaceAllLiteralString(path, dstPlatform)
	return path
}

func (e *elfInfo) verify(target *elfInfo) (bool, error) {
	// continue if other ELF has a different architecture
	if e.Class != target.Class || e.Machine != target.Machine {
		return false, nil
	}
	// abort if target ELF is not DYN or EXEC
	if target.Type != elf.ET_DYN && target.Type != elf.ET_EXEC {
		return false, fmt.Errorf("[ldd/verify] invalid ELF type for target: %s", target.Path)
	}
	// check for ELF header compatibility
	isValidABI := target.OSABI == elf.ELFOSABI_NONE || target.OSABI == elf.ELFOSABI_LINUX
	if e.Data != target.Data || e.Version != target.Version || !isValidABI {
		return false, fmt.Errorf("[ldd/verify] incompatible ELF headers for target: %s", target.Path)
	}
	return true, nil
}

type cacheABI int32

const (
	kABIFlagArm64_Glibc6  cacheABI = 0x0a00 | 0x0003
	kABIFlagX86_64_Glibc6 cacheABI = 0x0300 | 0x0003
)

func (e *elfInfo) ldCacheABI() cacheABI {
	if e.Class != elf.ELFCLASS64 || e.Data != elf.ELFDATA2LSB {
		return 0
	}
	switch e.Machine {
	case elf.EM_AARCH64:
		return kABIFlagArm64_Glibc6
	case elf.EM_X86_64:
		return kABIFlagX86_64_Glibc6
	default:
		return 0
	}
}

func readDynTable(f *elf.File) (map[elf.DynTag][]string, error) {
	var dyn *elf.Prog
	for _, p := range f.Progs {
		if p.Type == elf.PT_DYNAMIC {
			dyn = p
			break
		}
	}
	if dyn == nil {
		return nil, nil
	}

	data, err := io.ReadAll(dyn.Open())
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != dyn.Filesz {
		return nil, fmt.Errorf("[ldd/readDynTable] truncated PT_DYNAMIC")
	}

	type entry struct {
		tag elf.DynTag
		val uint64
	}
	var entries []entry
	var strtab, strsz uint64
	var hasStrtab, hasStrsz bool
	size := 16
	if f.Class == elf.ELFCLASS32 {
		size = 8
	}
	for i := 0; i < len(data); i += size {
		if len(data)-i < size {
			return nil, fmt.Errorf("[ldd/readDynTable] truncated dynamic entry")
		}
		var e entry
		if size == 16 {
			e = entry{elf.DynTag(f.ByteOrder.Uint64(data[i:])), f.ByteOrder.Uint64(data[i+8:])}
		} else {
			e = entry{elf.DynTag(f.ByteOrder.Uint32(data[i:])), uint64(f.ByteOrder.Uint32(data[i+4:]))}
		}
		if e.tag == elf.DT_NULL {
			break
		}
		switch e.tag {
		case elf.DT_STRTAB:
			strtab, hasStrtab = e.val, true
		case elf.DT_STRSZ:
			strsz, hasStrsz = e.val, true
		case elf.DT_NEEDED, elf.DT_SONAME, elf.DT_RPATH, elf.DT_RUNPATH:
			entries = append(entries, e)
		}
	}

	// Without string-valued entries the string table is never consulted.
	if len(entries) == 0 {
		return nil, nil
	}
	if !hasStrtab {
		return nil, fmt.Errorf("[ldd/readDynTable] DT_STRTAB is missing")
	}
	if !hasStrsz {
		return nil, fmt.Errorf("[ldd/readDynTable] DT_STRSZ is missing")
	}
	str, err := readVaddr(f, strtab, strsz)
	if err != nil {
		return nil, err
	}
	result := map[elf.DynTag][]string{}
	for _, e := range entries {
		if e.val >= uint64(len(str)) {
			return nil, fmt.Errorf("[ldd/readDynTable] string offset out of range: %d", e.val)
		}
		s := str[e.val:]
		if i := bytes.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		} else {
			return nil, fmt.Errorf("[ldd/readDynTable] unterminated string at offset %d", e.val)
		}
		result[e.tag] = append(result[e.tag], string(s))
	}
	return result, nil
}

func readVaddr(f *elf.File, addr, size uint64) ([]byte, error) {
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD || addr < p.Vaddr {
			continue
		}
		// Compare without addition so that crafted values cannot overflow.
		off := addr - p.Vaddr
		if size > p.Filesz || off > p.Filesz-size {
			continue
		}
		// ReadAll grows the buffer only as data arrives, so a bogus size cannot
		// force a huge allocation beyond the actual file contents.
		buf, err := io.ReadAll(io.NewSectionReader(p, int64(off), int64(size)))
		if err != nil {
			return nil, err
		}
		if uint64(len(buf)) != size {
			return nil, fmt.Errorf("[ldd/readVaddr] truncated data at vaddr 0x%x", addr)
		}
		return buf, nil
	}
	return nil, fmt.Errorf("[ldd/readVaddr] vaddr 0x%x not in any PT_LOAD", addr)
}
