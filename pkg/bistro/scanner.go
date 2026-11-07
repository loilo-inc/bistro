package bistro

import (
	"debug/elf"
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Scanner interface {
	Scan(fileOrDir rootfs.Path) ([]*ElfDependency, error)
}

type scanner struct {
	DI
	finder    Finder
	excludes  []rootfs.Path
	fileCache map[rootfs.Path]*ElfFile
	depsCache map[rootfs.Path]*ElfDependency
}

var _ Scanner = &scanner{}

type ScannerInput struct {
	Excludes []rootfs.Path
}

func NewScanner(di DI, input ScannerInput) Scanner {
	finder := NewFinder(di)
	return &scanner{
		DI:        di,
		finder:    finder,
		excludes:  input.Excludes,
		fileCache: map[rootfs.Path]*ElfFile{},
		depsCache: map[rootfs.Path]*ElfDependency{},
	}
}

type ElfFile struct {
	Path rootfs.Path
	Type elf.Type
}

type ElfDependency struct {
	ElfFile *ElfFile
	// Package that provides the ELF file itself, if available
	Package      *dpkg.Package
	Dependencies []*ExeDependency
	BrokenLibs   []string
}

func (s *scanner) Scan(path rootfs.Path) ([]*ElfDependency, error) {
	if path.IsExcluded(excludes) {
		return nil, nil
	}
	stat, err := fs.Stat(s.FS, path.Path())
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return s.scanDir(path, excludes)
	}
	if dep, err := s.scanFile(path); err != nil {
		return nil, err
	} else if dep != nil {
		return []*ElfDependency{dep}, nil
	}
	return nil, nil
}

func (s *scanner) scanDir(path rootfs.Path) ([]*ElfDependency, error) {
	elfFiles, err := s.findElfFiles(path, excludes)
	if err != nil {
		return nil, err
	}
	var result []*ElfDependency
	for _, elfFile := range elfFiles {
		deps, err := s.scanElfFile(elfFile)
		if err != nil {
			return nil, err
		}
		result = append(result, deps)
	}
	return result, nil
}

func (s *scanner) scanFile(path rootfs.Path) (*ElfDependency, error) {
	elfFile, isElf, err := s.handleFile(path)
	if !isElf {
		return nil, err
	}
	dep, err := s.scanElfFile(elfFile)
	if err != nil {
		return nil, err
	}
	if isElf {
		return dep, nil
	}
	return nil, nil
}

func (s *scanner) scanElfFile(file *ElfFile) (*ElfDependency, error) {
	if cache, ok := s.depsCache[file.Path]; ok {
		return cache, nil
	}
	result := &ElfDependency{
		ElfFile:      file,
		Dependencies: nil,
	}
	selfPkg, err := s.finder.FindPackageForExecutable(file.Path)
	if err != nil && err != errPackageNotFound {
		return nil, err
	}
	if selfPkg != nil {
		result.Package = selfPkg
	}
	trace, err := s.finder.TraceDepsForExecutable(file.Path)
	if err != nil {
		return nil, err
	}
	result.Dependencies = trace.Dependencies
	result.BrokenLibs = trace.BrokenLibs

	s.depsCache[file.Path] = result
	return result, nil
}

func (s *scanner) findElfFiles(dir rootfs.Path) ([]*ElfFile, error) {
	ertries, err := fs.ReadDir(s.FS, dir.Path())
	if err != nil {
		return nil, err
	}
	var result []*ElfFile
	for _, entry := range ertries {
		subPath := dir.Join(entry.Name())
		if subPath.IsExcluded(excludes) {
			continue
		}
		if entry.IsDir() {
			if subResult, err := s.findElfFiles(subPath, excludes); err != nil {
				return nil, err
			} else {
				result = append(result, subResult...)
			}
		} else {
			if file, isElf, err := s.handleFile(subPath); err != nil {
				return nil, err
			} else if isElf {
				result = append(result, file)
			}
		}
	}
	return result, nil
}

func (s *scanner) handleFile(path rootfs.Path) (*ElfFile, bool, error) {
	if cache, ok := s.fileCache[path]; ok {
		return cache, cache != nil, nil
	}
	file, err := s.FS.Open(path.Path())
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	elfFile, err := elf.NewFile(&rootfs.FsReaderAt{File: file})
	if _, ok := err.(*elf.FormatError); ok {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	defer elfFile.Close()
	if elfFile.Type == elf.ET_EXEC || elfFile.Type == elf.ET_DYN {
		ef := &ElfFile{
			Path: path,
			Type: elfFile.Type,
		}
		s.fileCache[path] = ef
		return ef, true, nil
	}
	s.fileCache[path] = nil
	return nil, false, nil
}
