package ldd

import (
	"debug/elf"
	"path/filepath"
	"regexp"
)

type elfInfo struct {
	*elfPath
	Path   string
	ABI    ABI
	SONAME string
	Needed []string
}

func readElf(path string, LIB string, PLATFORM string) (*elfInfo, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	abi, err := cacheFlags(file)
	if err != nil {
		return nil, err
	}

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
		elfPath: elfPath,
		Path:    path,
		ABI:     abi,
		SONAME:  soname,
		Needed:  dtNeeded,
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
