package ldd

import (
	"fmt"
	"io/fs"
	"path"
	"runtime"
	"strings"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/loilo-inc/bistro/pkg/util"
)

type Resolver interface {
	Load() error
	ListObjects(path string) ([]*Library, error)
}

type resolver struct {
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
	var sysLibraryPath []rootfs.Path
	if input.DstLib != "" {
		sysLibraryPath = append(sysLibraryPath,
			rootfs.NewPath(path.Join("", input.DstLib)),
			rootfs.NewPath(path.Join("usr", input.DstLib)),
		)
	}
	sysLibraryPath = append(sysLibraryPath,
		rootfs.NewPath("/lib"),
		rootfs.NewPath("/usr/lib"),
		rootfs.NewPath("/lib64"),
		rootfs.NewPath("/usr/lib64"),
	)
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
	rPaths []rootfs.Path
}

func (r *resolver) ListObjects(file string) ([]*Library, error) {
	if !path.IsAbs(file) {
		return nil, fmt.Errorf("file path is not absolute: %s", file)
	}
	rootPath := rootfs.NewPath(file)
	dstOrigin := rootPath.Dir()

	ldLibraryPath := expandVars(r.ldLibraryPath, dstOrigin.Path(), r.dstLib, r.dstPlatform)
	var ldLibraryPaths []rootfs.Path
	for _, p := range strings.Split(ldLibraryPath, ":") {
		ldLibraryPaths = append(ldLibraryPaths, rootfs.NewPath(p))
	}

	rr := &rootResolver{
		resolver:       r,
		ldLibraryPaths: ldLibraryPaths,
	}

	rootDeps, err := rr.listObjects(rootPath, nil)
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
