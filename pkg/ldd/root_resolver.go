package ldd

import (
	"debug/elf"
	"fmt"
	"path/filepath"

	"github.com/loilo-inc/bistro/pkg/util"
)

type rootResolver struct {
	*resolver
	ldLibraryPaths []string
	// lib -> loaded library
	loaded util.Map[string, *Library]
	// path -> list of dependent libraries
	graph util.Map[string, []*Library]
}

func (r *rootResolver) listObjects(path string, parentRPaths []string) (*searchResult, error) {

	elfInfo, err := r.readElf(path)
	if err != nil {
		return nil, err
	}

	var rPaths []string
	rPaths = append(rPaths, elfInfo.RPaths...)
	rPaths = append(rPaths, parentRPaths...)

	var results []*Library
	for _, lib := range elfInfo.Needed {
		found, err := r.findLibrary(lib, elfInfo, rPaths)
		if err != nil {
			return nil, err
		}
		results = append(results, found)
	}
	r.graph.Set(elfInfo.Path, results)

	return &searchResult{libs: results, rPaths: rPaths}, nil
}

func (r *rootResolver) findLibrary(origLib string, srcElf *elfInfo, rPaths []string) (*Library, error) {

	lib := expandVars(origLib, srcElf.Origin, r.dstLib, r.dstPlatform)

	// check load chache by requested library name
	if loaded, ok := r.loaded.LookUp(lib); ok {
		return loaded, nil
	}

	pr := &pathResolver{rootResolver: r, targetLib: lib, srcElf: srcElf}
	resolvedPath, pathFound, err := pr.resolve(rPaths)
	if err != nil {
		return nil, err
	}
	// stop registering cache if the library cannot be resolved
	if !pathFound {
		return &Library{Name: lib}, nil
	}

	if absPath, err := filepath.Abs(resolvedPath); err != nil {
		return nil, err
	} else {
		resolvedPath = absPath
	}

	libElf, err := r.readElf(resolvedPath)
	if err != nil {
		return nil, err
	}

	found := &Library{
		Name: lib,
		Path: resolvedPath,
	}

	// doubly cache the library by both its requested name and SONAME (if available)
	r.loaded.Set(lib, found)
	if libElf.SONAME != "" {
		if !r.loaded.Has(libElf.SONAME) {
			r.loaded.Set(libElf.SONAME, found)
		}
	}

	return found, nil
}

func (r *rootResolver) readElf(path string) (*elfInfo, error) {
	if cached, ok := r.elfCache.LookUp(path); ok {
		return cached, nil
	}
	file, err := r.fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	elfFile, err := elf.NewFile(util.NewReaderAt(file))
	if err != nil {
		return nil, fmt.Errorf("[ldd/readElf] malformed ELF file: %s: %w", path, err)
	}
	info, err := newElf(elfFile, path, r.dstLib, r.dstPlatform)
	if err != nil {
		return nil, fmt.Errorf("[ldd/readElf] malformed ELF file: %s: %w", path, err)
	}
	r.elfCache.Set(path, info)
	return info, nil
}
