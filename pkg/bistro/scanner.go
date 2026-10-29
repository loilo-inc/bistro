package bistro

import (
	"debug/elf"
	"os"
	"path/filepath"
)

type scanner struct {
	ldd       Ldd
	dpkg      Dpkg
	finder    Finder
	fileCache map[string]*ElfFile
	depsCache map[string]*ElfDependency
}

func CreateScanner(ldd Ldd, dpkg Dpkg) *scanner {
	finder := CreateFinder(dpkg, ldd)
	return &scanner{
		ldd:       ldd,
		dpkg:      dpkg,
		finder:    finder,
		fileCache: map[string]*ElfFile{},
		depsCache: map[string]*ElfDependency{},
	}
}

type ElfFile struct {
	Path     string
	Type     elf.Type
	NeedsLdd bool
}

type ElfDependency struct {
	ElfFile      *ElfFile
	Dependencies []*ExeDependency
	BrokenLibs   []string
}

func (s *scanner) Scan(origPath string) ([]*ElfDependency, error) {
	origPathAbs, err := filepath.Abs(origPath)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(origPathAbs)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return s.ScanDir(origPathAbs)
	}
	elfFile, isElf, err := s.handleFile(origPathAbs)
	if !isElf {
		return nil, err
	}
	dep, err := s.ScanFile(elfFile)
	if err != nil {
		return nil, err
	}
	if isElf {
		return []*ElfDependency{dep}, nil
	}
	return nil, nil
}

func (s *scanner) ScanDir(dir string) ([]*ElfDependency, error) {
	elfFiles, err := s.findElfFiles(dir)
	if err != nil {
		return nil, err
	}
	var result []*ElfDependency
	for _, elfFile := range elfFiles {
		deps, err := s.ScanFile(elfFile)
		if err != nil {
			return nil, err
		}
		result = append(result, deps)
	}
	return result, nil
}

func (s *scanner) ScanFile(file *ElfFile) (*ElfDependency, error) {
	if cache, ok := s.depsCache[file.Path]; ok {
		return cache, nil
	}
	result := &ElfDependency{
		ElfFile:      file,
		Dependencies: nil,
	}
	if file.NeedsLdd {
		trace, err := s.finder.TraceDepsForExecutable(file.Path)
		if err != nil {
			return nil, err
		}
		result.Dependencies = trace.Dependencies
		result.BrokenLibs = trace.BrokenLibs
	}
	s.depsCache[file.Path] = result
	return result, nil
}

func (s *scanner) findElfFiles(dir string) ([]*ElfFile, error) {
	ertries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []*ElfFile
	for _, entry := range ertries {
		if entry.IsDir() {
			if subResult, err := s.findElfFiles(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			} else {
				result = append(result, subResult...)
			}
		} else {
			if file, isElf, err := s.handleFile(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			} else if isElf {
				result = append(result, file)
			}
		}
	}
	return result, nil
}

func (s *scanner) handleFile(path string) (*ElfFile, bool, error) {
	if cache, ok := s.fileCache[path]; ok {
		return cache, cache != nil, nil
	}
	elfFile, err := elf.Open(path)
	if _, ok := err.(*elf.FormatError); ok {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	defer elfFile.Close()
	if elfFile.Type == elf.ET_EXEC || elfFile.Type == elf.ET_DYN {
		needsLdd, err := needsLdd(elfFile)
		if err != nil {
			return nil, false, err
		}
		ef := &ElfFile{
			Path:     path,
			Type:     elfFile.Type,
			NeedsLdd: needsLdd,
		}
		s.fileCache[path] = ef
		return ef, true, nil
	}
	s.fileCache[path] = nil
	return nil, false, nil
}

func needsLdd(file *elf.File) (bool, error) {
	libs, err := file.ImportedLibraries()
	if err != nil {
		return false, err
	}
	if len(libs) > 0 {
		return true, nil
	}
	for _, p := range file.Progs {
		if p.Type == elf.PT_INTERP {
			return true, nil
		}
	}
	return false, nil
}
