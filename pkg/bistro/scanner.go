package bistro

import (
	"debug/elf"
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Scanner interface {
	Scan(file rootfs.Path) ([]*ElfDependency, error)
	SetExcludes(excludes rootfs.PathSet)
}

type scanner struct {
	DI
	finder    Finder
	excludes  rootfs.PathSet
	fileCache map[rootfs.Path]*ElfFile
	depsCache map[rootfs.Path]*ElfDependency
}

var _ Scanner = &scanner{}

func NewScanner(di DI) Scanner {
	finder := NewFinder(di)
	return &scanner{
		DI:        di,
		finder:    finder,
		excludes:  rootfs.PathSet{},
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
	var result []*ElfDependency
	onStart := func(path rootfs.Path) (bool, error) {
		skip, err := s.excludes.Match(s.FS, path)
		if err != nil {
			return false, err
		}
		return !skip, nil
	}
	onVisit := func(path rootfs.Path, link string, info fs.FileInfo) error {
		if !info.Mode().IsRegular() {
			return nil
		}
		dep, err := s.scanFile(path)
		if err != nil {
			return err
		}
		if dep != nil {
			result = append(result, dep)
		}
		return nil
	}
	traverser := rootfs.NewTraverser(s.FS, onStart, onVisit)
	err := traverser.Traverse(path)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *scanner) SetExcludes(excludes rootfs.PathSet) {
	s.excludes = excludes
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
