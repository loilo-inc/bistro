package ldd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/loilo-inc/bistro/pkg/util"
)

type Resolver interface {
	Load() error
	ListObjects(path string) ([]*Library, error)
}

type resolver struct {
	ldLibraryPath   string
	sysLibraryPaths []string
	lib             string
	platform        string
	ldSoCache       LdCache
}

type rootResolver struct {
	*resolver
	ldLibraryPaths []string
	elfCache       util.Map[string, *elfInfo]
	// lib -> loaded library
	loaded util.Map[string, *Library]
	// path -> list of dependent libraries
	graph util.Map[string, []*Library]
}

type Library struct {
	Name string
	Path string
	Deps []*Library
}

func (l *Library) IsBroken() bool {
	return l.Path == ""
}

type ResolverOptions struct {
	LdLibraryPath  string
	SysLibraryPath string
	Lib            string
	Platform       string
}

func DefaultResolver() (Resolver, error) {
	vars, err := getLoaderVars(runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	return NewResolver(ResolverOptions{
		LdLibraryPath:  os.Getenv("LD_LIBRARY_PATH"),
		SysLibraryPath: "/lib:/usr/lib:/lib64:/usr/lib64",
		Lib:            vars.Lib,
		Platform:       vars.Platform,
	}), nil
}

func NewResolver(input ResolverOptions) *resolver {
	return &resolver{
		ldLibraryPath:   input.LdLibraryPath,
		sysLibraryPaths: filepath.SplitList(input.SysLibraryPath),
		lib:             input.Lib,
		platform:        input.Platform,
	}
}

func (r *resolver) Load() error {
	return r.ldSoCache.Load()
}

type searchResult struct {
	libs   []*Library
	rPaths []string
}

func (r *resolver) ListObjects(path string) ([]*Library, error) {

	ldLibraryPaths := filepath.SplitList(
		expandVars(r.ldLibraryPath, filepath.Dir(path), r.lib, r.platform),
	)
	rr := &rootResolver{
		resolver:       r,
		ldLibraryPaths: ldLibraryPaths,
	}

	rootDeps, err := rr.listObjects(path, nil)
	if err != nil {
		return nil, err
	}

	queue := []*searchResult{rootDeps}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, lib := range current.libs {
			if lib.IsBroken() {
				continue
			}
			if cached, ok := rr.graph.LookUp(lib.Path); ok {
				lib.Deps = cached
				continue
			}
			deps, err := rr.listObjects(lib.Path, current.rPaths)
			if err != nil {
				return nil, err
			}
			lib.Deps = deps.libs
			queue = append(queue, deps)
		}
	}
	return rootDeps.libs, nil
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

	lib := expandVars(origLib, srcElf.Origin, r.lib, r.platform)

	// check load chache by requested library name
	if loaded, ok := r.loaded.LookUp(lib); ok {
		return loaded, nil
	}

	var resolvedPath string
	// DT_NEEDED that contains a path component
	if strings.ContainsRune(lib, filepath.Separator) {
		absPath, err := filepath.Abs(lib)
		if err != nil {
			return nil, err
		}
		resolvedPath, _ = resolvePath(absPath)
	} else if srcElf.HasRunPath {
		// ignore rPath inheritance when DT_RUNPATH is present
		resolvedPath = r.resolveLibrary(lib, srcElf, nil)
	} else {
		resolvedPath = r.resolveLibrary(lib, srcElf, rPaths)
	}
	// stop registering cache if the library cannot be resolved
	if resolvedPath == "" {
		return &Library{Name: lib}, nil
	}

	if abPath, err := filepath.Abs(resolvedPath); err != nil {
		return nil, err
	} else {
		resolvedPath = abPath
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
	if elf, ok := r.elfCache.LookUp(path); ok {
		return elf, nil
	}
	elf, err := readElf(path, r.lib, r.platform)
	if err != nil {
		return nil, err
	}
	r.elfCache.Set(path, elf)
	return elf, nil
}

func (r *rootResolver) resolveLibrary(lib string, srcElf *elfInfo, rPaths []string) string {
	// 1: use DT_RPATH, if DT_RUNPATH not present
	if len(srcElf.RunPaths) == 0 {
		if path, ok := resolveLibraryPath(lib, rPaths); ok {
			return path
		}
	}
	// 2: use LD_LIBRARY_PATH
	if path, ok := resolveLibraryPath(lib, r.ldLibraryPaths); ok {
		return path
	}
	// 3: use DT_RUNPATH
	if path, ok := resolveLibraryPath(lib, srcElf.RunPaths); ok {
		return path
	}
	// 4: use ld.so.cache
	if path, ok := r.ldSoCache.ResolveLibrary(lib, srcElf.ABI); ok {
		return path
	}
	// 5: use default system library paths
	if path, ok := resolveLibraryPath(lib, r.sysLibraryPaths); ok {
		return path
	}
	return ""
}

func resolveLibraryPath(lib string, paths []string) (string, bool) {
	var targets []string
	for _, p := range paths {
		target := filepath.Join(p, lib)
		targets = append(targets, target)
	}
	return resolvePath(targets...)
}

func resolvePath(paths ...string) (string, bool) {
	for _, p := range paths {
		stat, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", false
		}
		if stat.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}
