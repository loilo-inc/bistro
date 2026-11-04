package ldd

import (
	"io/fs"
	"path"
	"runtime"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type Resolver interface {
	Load() error
	ListObjects(path rootfs.Path) ([]*Library, error)
}

type resolver struct {
	cwd             rootfs.Path
	ldLibraryPath   string
	sysLibraryPaths []rootfs.Path
	dstLib          string
	dstPlatform     string
	elfCache        util.Map[rootfs.Path, *elfInfo]
	ldSoCache       LdCache
	fs              fs.FS
}

var _ Resolver = (*resolver)(nil)

type ResolverOptions struct {
	Cwd           string
	FS            fs.FS
	LdLibraryPath string
	// $LIB Dynamic String Token
	DstLib string
	// $PLATFORM Dynamic String Token
	DstPlatform string
}

func NewResolver(input ResolverOptions) Resolver {
	if input.DstLib == "" {
		input.DstLib = getDstLib(runtime.GOARCH)
	}
	if input.DstPlatform == "" {
		input.DstPlatform = getDstPlatform(runtime.GOARCH)
	}
	var sysLibraryPath []string
	if input.DstLib != "" {
		sysLibraryPath = append(sysLibraryPath,
			path.Join("/", input.DstLib),
			path.Join("/usr", input.DstLib),
		)
	}
	sysLibraryPath = append(sysLibraryPath,
		"/lib", "/usr/lib", "/lib64", "/usr/lib64",
	)
	return &resolver{
		cwd:             rootfs.MustPath(input.Cwd),
		ldLibraryPath:   input.LdLibraryPath,
		sysLibraryPaths: rootfs.MustPaths(sysLibraryPath...),
		dstLib:          input.DstLib,
		dstPlatform:     input.DstPlatform,
		ldSoCache:       NewLdCache(input.FS),
		fs:              input.FS,
	}
}

func (r *resolver) Load() error {
	return r.ldSoCache.Load()
}

type searchResult struct {
	libs   []*Library
	rPaths []rootfs.Path
}

func (r *resolver) ListObjects(exePath rootfs.Path) ([]*Library, error) {
	exeDir := exePath.Dir()

	var ldLibraryPaths []rootfs.Path
	if r.ldLibraryPath != "" {
		expanded := expandVars(r.ldLibraryPath, exeDir.Abs(), r.dstLib, r.dstPlatform)
		ldLibraryPaths = r.cwd.SplitList(expanded)
	}

	rr := &rootResolver{
		resolver:       r,
		ldLibraryPaths: ldLibraryPaths,
	}

	rootDeps, err := rr.listObjects(exePath, nil)
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
