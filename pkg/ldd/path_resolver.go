package ldd

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type pathResolver struct {
	*rootResolver
	targetLib string
	srcElf    *elfInfo
}

func (p *pathResolver) resolve(rPaths []rootfs.Path) (*rootfs.Path, bool, error) {
	// DT_NEEDED that contains a path component
	if strings.ContainsRune(p.targetLib, '/') {
		return p.resolvePath([]rootfs.Path{p.cwd.Resolve(p.targetLib)})
	}
	// 1: use DT_RPATH, if DT_RUNPATH not present
	if !p.srcElf.HasRunPath {
		if path, ok, err := p.resolveLibraryPath(rPaths); err != nil {
			return nil, false, err
		} else if ok {
			return path, true, nil
		}
	}
	// 2: use LD_LIBRARY_PATH
	if path, ok, err := p.resolveLibraryPath(p.ldLibraryPaths); err != nil {
		return nil, false, err
	} else if ok {
		return path, true, nil
	}
	// 3: use DT_RUNPATH
	if path, ok, err := p.resolveLibraryPath(p.srcElf.RunPaths); err != nil {
		return nil, false, err
	} else if ok {
		return path, true, nil
	}
	// 4: use ld.so.cache
	if path, ok, err := p.lookupLdCache(); err != nil {
		return nil, false, err
	} else if ok {
		return path, true, nil
	}
	// 5: use default system library paths
	if path, ok, err := p.resolveLibraryPath(p.sysLibraryPaths); err != nil {
		return nil, false, err
	} else if ok {
		return path, true, nil
	}
	return nil, false, nil
}

func (p *pathResolver) resolveLibraryPath(paths []rootfs.Path) (*rootfs.Path, bool, error) {
	var targets []rootfs.Path
	for _, path := range paths {
		target := path.Resolve(p.targetLib)
		targets = append(targets, target)
	}
	return p.resolvePath(targets)
}

func (p *pathResolver) lookupLdCache() (*rootfs.Path, bool, error) {
	path, found := p.ldSoCache.ResolveLibrary(p.targetLib, p.srcElf.ldCacheABI())
	if !found {
		return nil, false, nil
	}
	return p.resolvePath(rootfs.Paths(path))
}

func (p *pathResolver) resolvePath(paths []rootfs.Path) (*rootfs.Path, bool, error) {
	for _, path := range paths {
		stat, err := os.Stat(path.Abs())
		if err != nil {
			continue
		}
		if !stat.Mode().IsRegular() {
			continue
		}
		if ok, err := p.verify(path); err != nil {
			return nil, false, err
		} else if ok {
			return &path, true, nil
		}
	}
	return nil, false, nil
}

func (p *pathResolver) verify(path rootfs.Path) (bool, error) {
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
