package bistro

import (
	"debug/elf"
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type Scanner interface {
	Scan(origPath string) ([]*ElfDependency, error)
}

type scanner struct {
	DI
	finder    Finder
	fileCache util.Map[rootfs.Path, *ElfFile]
	depsCache util.Map[rootfs.Path, *ElfDependency]
}

var _ Scanner = &scanner{}

func NewScanner(di DI) Scanner {
	finder := NewFinder(di)
	return &scanner{
		DI:     di,
		finder: finder,
	}
}

type ElfFile struct {
	Path     rootfs.Path
	Type     elf.Type
	NeedsLdd bool
}

type ElfDependency struct {
	ElfFile *ElfFile
	// Package that provides the ELF file itself, if available
	Package      dpkg.Package
	Dependencies []*ExeDependency
	BrokenLibs   []string
}

func (s *scanner) Scan(origPath string) ([]*ElfDependency, error) {
	path := rootfs.NewPath(origPath)
	stat, err := fs.Stat(s.FS, path.Path())
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return s.scanDir(path)
	}
	elfFile, isElf, err := s.handleFile(path)
	if !isElf {
		return nil, err
	}
	dep, err := s.scanFile(elfFile)
	if err != nil {
		return nil, err
	}
	if isElf {
		return []*ElfDependency{dep}, nil
	}
	return nil, nil
}

func (s *scanner) scanDir(dir rootfs.Path) ([]*ElfDependency, error) {
	elfFiles, err := s.findElfFiles(dir)
	if err != nil {
		return nil, err
	}
	var result []*ElfDependency
	for _, elfFile := range elfFiles {
		deps, err := s.scanFile(elfFile)
		if err != nil {
			return nil, err
		}
		result = append(result, deps)
	}
	return result, nil
}

func (s *scanner) scanFile(file *ElfFile) (*ElfDependency, error) {
	if cache, ok := s.depsCache.LookUp(file.Path); ok {
		return cache, nil
	}
	result := &ElfDependency{
		ElfFile:      file,
		Dependencies: nil,
	}
	selfPkg, err := s.finder.FindPackageForExecutable(file.Path.Abs())
	if err != nil && err != errPackageNotFound {
		return nil, err
	}
	if selfPkg != nil {
		result.Package = selfPkg
	}
	if file.NeedsLdd {
		trace, err := s.finder.TraceDepsForExecutable(file.Path.Abs())
		if err != nil {
			return nil, err
		}
		result.Dependencies = trace.Dependencies
		result.BrokenLibs = trace.BrokenLibs
	}
	s.depsCache.Set(file.Path, result)
	return result, nil
}

func (s *scanner) findElfFiles(dir rootfs.Path) ([]*ElfFile, error) {
	ertries, err := fs.ReadDir(s.FS, dir.Path())
	if err != nil {
		return nil, err
	}
	var result []*ElfFile
	for _, entry := range ertries {
		if entry.IsDir() {
			if subResult, err := s.findElfFiles(dir.Join(entry.Name())); err != nil {
				return nil, err
			} else {
				result = append(result, subResult...)
			}
		} else {
			if file, isElf, err := s.handleFile(dir.Join(entry.Name())); err != nil {
				return nil, err
			} else if isElf {
				result = append(result, file)
			}
		}
	}
	return result, nil
}

func (s *scanner) handleFile(path rootfs.Path) (*ElfFile, bool, error) {
	if cache, ok := s.fileCache.LookUp(path); ok {
		return cache, cache != nil, nil
	}
	file, err := s.FS.Open(path.Path())
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	elfFile, err := elf.NewFile(util.NewReaderAt(file))
	if _, ok := err.(*elf.FormatError); ok {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	defer elfFile.Close()
	if elfFile.Type == elf.ET_EXEC || elfFile.Type == elf.ET_DYN {
		ef := &ElfFile{
			Path:     path,
			Type:     elfFile.Type,
			NeedsLdd: needsLdd(elfFile),
		}
		s.fileCache.Set(path, ef)
		return ef, true, nil
	}
	s.fileCache.Set(path, nil)
	return nil, false, nil
}

func needsLdd(file *elf.File) bool {
	for _, p := range file.Progs {
		if p.Type == elf.PT_DYNAMIC {
			return true
		}
	}
	return false
}
