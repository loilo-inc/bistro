package bistro

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinderTraceDependencies(t *testing.T) {
	dir := rootFSTempDir(t)
	first, second := filepath.Join(dir, "libone.so"), filepath.Join(dir, "libtwo.so")
	writeRootFSFile(t, first, "one", 0644)
	writeRootFSFile(t, second, "two", 0644)
	alias := filepath.Join(dir, "alias.so")
	require.NoError(t, os.Symlink("libone.so", alias))
	lddCalls, lookups := 0, 0
	pkg := testPackage(t, "shared:arm64", "", "", "unpacked")
	q := stubQuery{lookup: func(path string) (*dpkg.Package, error) {
		lookups++
		assert.Contains(t, []string{first, second}, path)
		return pkg, nil
	}}
	libs := []*ldd.Library{{Name: "one", Path: alias}, {Name: "alias", Path: first}, {Name: "missing"}, {Name: "two", Path: second}}
	f := NewFinder(DI{Query: q, Ldd: stubLdd(func(path string) ([]*ldd.Library, error) {
		lddCalls++
		assert.Contains(t, []string{"/bin/one", "/bin/two"}, path)
		return libs, nil
	})})
	for _, path := range []string{"/bin/one", "/bin/one", "/bin/two"} {
		result, err := f.TraceDepsForExecutable(path)
		require.NoError(t, err)
		assert.Equal(t, []string{"missing"}, result.BrokenLibs)
		require.Len(t, result.Dependencies, 3)
		for i, lib := range []*ldd.Library{libs[0], libs[1], libs[3]} {
			assert.Same(t, lib, result.Dependencies[i].Library)
			assert.Same(t, pkg, result.Dependencies[i].Package)
		}
	}
	assert.Equal(t, 3, lddCalls, "each trace starts a separate resolver traversal")
	assert.Equal(t, 9, lookups, "look up each resolved dependency through the query index")
}

func TestFinderFailures(t *testing.T) {
	lib := filepath.Join(rootFSTempDir(t), "library")
	writeRootFSFile(t, lib, "library", 0644)
	failure := errors.New("query failed")
	for _, tc := range []struct {
		name, path        string
		lddErr, lookupErr error
		want              string
	}{
		{name: "relative executable", path: "relative", want: "file path is not absolute"},
		{name: "ldd failure", lddErr: failure, want: failure.Error()},
		{name: "lookup failure", lookupErr: failure, want: failure.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "/bin/app"
			}
			q := stubQuery{lookup: func(string) (*dpkg.Package, error) { return nil, tc.lookupErr }}
			f := NewFinder(DI{Query: q, Ldd: stubLdd(func(string) ([]*ldd.Library, error) { return []*ldd.Library{{Path: lib}}, tc.lddErr })})
			result, err := f.TraceDepsForExecutable(path)
			assert.Nil(t, result)
			require.ErrorContains(t, err, tc.want)
			if tc.lddErr != nil || tc.lookupErr != nil {
				assert.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestFinderRetriesFailedCommands(t *testing.T) {
	lib := filepath.Join(rootFSTempDir(t), "library")
	writeRootFSFile(t, lib, "library", 0644)
	for _, stage := range []string{"ldd", "lookup"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("transient failure")
			calls := map[string]int{}
			fail := func(name string) bool { calls[name]++; return name == stage && calls[name] == 1 }
			pkg := testPackage(t, "pkg", "", "", "installed")
			q := stubQuery{lookup: func(string) (*dpkg.Package, error) {
				if fail("lookup") {
					return nil, failure
				}
				return pkg, nil
			}}
			f := NewFinder(DI{Query: q, Ldd: stubLdd(func(string) ([]*ldd.Library, error) {
				if fail("ldd") {
					return nil, failure
				}
				return []*ldd.Library{{Path: lib}}, nil
			})})
			_, err := f.TraceDepsForExecutable("/bin/app")
			require.ErrorIs(t, err, failure)
			result, err := f.TraceDepsForExecutable("/bin/app")
			require.NoError(t, err)
			require.Len(t, result.Dependencies, 1)
			assert.Same(t, pkg, result.Dependencies[0].Package)
			assert.Equal(t, 2, calls[stage])
		})
	}
}

func TestFinderUnownedLibrary(t *testing.T) {
	lib := filepath.Join(rootFSTempDir(t), "library.so")
	writeRootFSFile(t, lib, "library", 0644)
	library := &ldd.Library{Name: "library.so", Path: lib}
	q := stubQuery{lookup: func(path string) (*dpkg.Package, error) {
		assert.Equal(t, lib, path)
		return nil, nil
	}}
	f := NewFinder(DI{Query: q, Ldd: stubLdd(func(string) ([]*ldd.Library, error) { return []*ldd.Library{library}, nil })})
	result, err := f.TraceDepsForExecutable("/bin/app")
	require.NoError(t, err)
	assert.Empty(t, result.BrokenLibs)
	require.Len(t, result.Dependencies, 1)
	assert.Same(t, library, result.Dependencies[0].Library)
	assert.Nil(t, result.Dependencies[0].Package)
}
