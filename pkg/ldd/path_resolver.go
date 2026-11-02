package ldd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type pathResolver struct {
	*rootResolver
	targetLib string
	srcElf    *elfInfo
}

func (p *pathResolver) resolve(rPaths []string) (string, bool, error) {
	// DT_NEEDED that contains a path component
	if strings.ContainsRune(p.targetLib, filepath.Separator) {
		absPath, err := filepath.Abs(p.targetLib)
		if err != nil {
			return "", false, err
		}
		return p.resolvePath([]string{absPath})
	}
	// 1: use DT_RPATH, if DT_RUNPATH not present
	if !p.srcElf.HasRunPath {
		if path, ok, err := p.resolveLibraryPath(rPaths); err != nil {
			return "", false, err
		} else if ok {
			return path, true, nil
		}
	}
	// 2: use LD_LIBRARY_PATH
	if path, ok, err := p.resolveLibraryPath(p.ldLibraryPaths); err != nil {
		return "", false, err
	} else if ok {
		return path, true, nil
	}
	// 3: use DT_RUNPATH
	if path, ok, err := p.resolveLibraryPath(p.srcElf.RunPaths); err != nil {
		return "", false, err
	} else if ok {
		return path, true, nil
	}
	// 4: use ld.so.cache
	if path, ok := p.ldSoCache.ResolveLibrary(p.targetLib, p.srcElf.ldCacheABI()); ok {
		return path, true, nil
	}
	// 5: use default system library paths
	if path, ok, err := p.resolveLibraryPath(p.sysLibraryPaths); err != nil {
		return "", false, err
	} else if ok {
		return path, true, nil
	}
	return "", false, nil
}

func (p *pathResolver) resolveLibraryPath(paths []string) (string, bool, error) {
	var targets []string
	for _, path := range paths {
		target := filepath.Join(path, p.targetLib)
		targets = append(targets, target)
	}
	return p.resolvePath(targets)
}

func (p *pathResolver) resolvePath(paths []string) (string, bool, error) {
	for _, path := range paths {
		stat, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !stat.Mode().IsRegular() {
			continue
		}
		if ok, err := p.verify(path); err != nil {
			return "", false, err
		} else if ok {
			return path, true, nil
		}
	}
	return "", false, nil
}

func (p *pathResolver) verify(path string) (bool, error) {
	dstElf, err := p.readElf(path)
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok && pathErr.Op == "open" {
		// ignore open error)
		return false, nil
	} else if err != nil {
		// abort on ELF format errors
		return false, err
	}
	return p.srcElf.verify(dstElf)
}
