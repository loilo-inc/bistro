package ldd

import (
	"debug/elf"
	"fmt"
	"path/filepath"
	"regexp"
)

type elfInfo struct {
	elf.FileHeader
	*elfPath
	Path   string
	SONAME string
	Needed []string
}

func readElf(path string, LIB string, PLATFORM string) (*elfInfo, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return newElf(file, path, LIB, PLATFORM)
}

func newElf(file *elf.File, path string, LIB string, PLATFORM string) (*elfInfo, error) {
	dtNeeded, err := file.DynString(elf.DT_NEEDED)
	if err != nil {
		return nil, err
	}
	dtSoname, err := file.DynString(elf.DT_SONAME)
	if err != nil {
		return nil, err
	}

	var soname string
	if len(dtSoname) > 0 {
		soname = dtSoname[0]
	}

	elfPath, err := expandElfPaths(path, file, LIB, PLATFORM)
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
	RPaths     []string
	RunPaths   []string
	HasRunPath bool
}

func expandElfPaths(path string, file *elf.File, LIB string, PLATFORM string) (*elfPath, error) {
	var rPaths []string
	var runPaths []string

	dtRPath, err := file.DynString(elf.DT_RPATH)
	if err != nil {
		return nil, err
	}
	dtRunPath, err := file.DynString(elf.DT_RUNPATH)
	if err != nil {
		return nil, err
	}

	origin := filepath.Dir(path)
	hasRunPath := len(dtRunPath) > 0

	// only evalueate DT_RPATH if DT_RUNPATH is not present
	if !hasRunPath {
		for _, p := range dtRPath {
			exp := expandVars(p, origin, LIB, PLATFORM)
			paths := filepath.SplitList(exp)
			rPaths = append(rPaths, paths...)
		}
	}

	for _, p := range dtRunPath {
		exp := expandVars(p, origin, LIB, PLATFORM)
		paths := filepath.SplitList(exp)
		runPaths = append(runPaths, paths...)
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

func expandVars(path string, origin string, lib string, platform string) string {
	path = originPat.ReplaceAllLiteralString(path, origin)
	path = libPat.ReplaceAllLiteralString(path, lib)
	path = platformPat.ReplaceAllLiteralString(path, platform)
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
