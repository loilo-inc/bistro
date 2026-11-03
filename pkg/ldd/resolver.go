package ldd

import (
	"io/fs"
	"path"
	"path/filepath"
	"runtime"

	"github.com/loilo-inc/bistro/pkg/util"
)

type Resolver interface {
	Load() error
	ListObjects(path string) ([]*Library, error)
}

type resolver struct {
	ldLibraryPath   string
	sysLibraryPaths []string
	dstLib          string
	dstPlatform     string
	elfCache        util.Map[string, *elfInfo]
	ldSoCache       LdCache
	fs              fs.FS
}

var _ Resolver = (*resolver)(nil)

type ResolverOptions struct {
	LdLibraryPath string
	// $LIB Dynamic String Token
	DstLib string
	// $PLATFORM Dynamic String Token
	DstPlatform string
}

func NewResolver(fs fs.FS, input ResolverOptions) Resolver {
	if input.DstLib == "" {
		input.DstLib = getDstLib(runtime.GOARCH)
	}
	if input.DstPlatform == "" {
		input.DstPlatform = getDstPlatform(runtime.GOARCH)
	}
	var sysLibraryPath []string
	if input.DstLib != "" {
		sysLibraryPath = append(sysLibraryPath,
			path.Join("", input.DstLib),
			path.Join("usr", input.DstLib),
		)
	}
	sysLibraryPath = append(sysLibraryPath, "/lib", "/usr/lib", "/lib64", "/usr/lib64")
	return &resolver{
		ldLibraryPath:   input.LdLibraryPath,
		sysLibraryPaths: sysLibraryPath,
		dstLib:          input.DstLib,
		dstPlatform:     input.DstPlatform,
		ldSoCache:       NewLdCache(fs),
		fs:              fs,
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

	ldLibraryPath := expandVars(r.ldLibraryPath, filepath.Dir(path), r.dstLib, r.dstPlatform)
	ldLibraryPaths := filepath.SplitList(ldLibraryPath)

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
