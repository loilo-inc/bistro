package bistro

import (
	"debug/elf"
	"os"
	"path/filepath"
)

type scanner struct {
	ldd  Ldd
	dpkg Dpkg
}

func CreateScanner(ldd Ldd, dpkg Dpkg) *scanner {
	return &scanner{
		ldd:  ldd,
		dpkg: dpkg,
	}
}

type ElfFile struct {
	Path string
	Type elf.Type
}

type ElfDependency struct {
	ElfFile      *ElfFile
	Dependencies []*ExeDependency
}

func (s *scanner) Scan(path string) ([]*ElfDependency, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return s.ScanDir(path)
	}
	dep, isElf, err := s.ScanFile(path)
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
		deps, isElf, err := s.ScanFile(elfFile.Path)
		if err != nil {
			return nil, err
		}
		if isElf {
			result = append(result, deps)
		}
	}
	return result, nil
}

func (s *scanner) ScanFile(path string) (*ElfDependency, bool, error) {
	finder := CreateFinder(s.dpkg, s.ldd)
	if file, err := s.handleFile(path); err != nil {
		return nil, false, err
	} else if file != nil {
		pkgs, err := finder.TraceDepsForExecutable(file.Path)
		if err != nil {
			return nil, false, err
		}
		return &ElfDependency{
			ElfFile:      file,
			Dependencies: pkgs,
		}, true, nil
	}
	return nil, false, nil
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
			if file, err := s.handleFile(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			} else if file != nil {
				result = append(result, file)
			}
		}
	}
	return result, nil
}

func (s *scanner) handleFile(path string) (*ElfFile, error) {
	elfFile, err := elf.Open(path)
	if _, ok := err.(*elf.FormatError); ok {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer elfFile.Close()
	if elfFile.Type == elf.ET_EXEC || elfFile.Type == elf.ET_DYN {
		return &ElfFile{Path: path, Type: elfFile.Type}, nil
	}
	return nil, nil
}
