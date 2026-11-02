package ldd

import (
	"os"
	"path/filepath"
	"runtime"
)

type Resolver interface {
	Load() error
	ListObjects(path string) ([]*Library, error)
}

type resolver struct {
	ldLibraryPath   string
	sysLibraryPaths []string
	tokenLib        string
	tokenPlatform   string
	ldSoCache       LdCache
}

var _ Resolver = (*resolver)(nil)

type Library struct {
	Name string
	Path string
	Deps []*Library
}

func (l *Library) IsBroken() bool {
	return l.Path == ""
}

type ResolverOptions struct {
	LdLibraryPath string
	Lib           string
	Platform      string
}

func DefaultResolver() (Resolver, error) {
	vars, err := getLoaderVars(runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	return NewResolver(ResolverOptions{
		LdLibraryPath: os.Getenv("LD_LIBRARY_PATH"),
		Lib:           vars.Lib,
		Platform:      vars.Platform,
	}), nil
}

func NewResolver(input ResolverOptions) *resolver {
	return &resolver{
		ldLibraryPath: input.LdLibraryPath,
		sysLibraryPaths: []string{
			filepath.Join("/", input.Lib),
			filepath.Join("/usr", input.Lib),
			"/lib", "/usr/lib", "/lib64", "/usr/lib64"},
		tokenLib:      input.Lib,
		tokenPlatform: input.Platform,
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

	ldLibraryPath := expandVars(r.ldLibraryPath, filepath.Dir(path), r.tokenLib, r.tokenPlatform)
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
