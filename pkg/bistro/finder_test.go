package bistro

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinderTraceAndCaches(t *testing.T) {
	dir := rootFSTempDir(t)
	first, second := filepath.Join(dir, "libone.so"), filepath.Join(dir, "libtwo.so")
	writeRootFSFile(t, first, "one", 0644)
	writeRootFSFile(t, second, "two", 0644)
	alias := filepath.Join(dir, "alias.so")
	require.NoError(t, os.Symlink("libone.so", alias))
	lddCalls, searches := 0, 0
	pkg := testPackage(t, "shared:arm64", "", "", "unpacked")
	q := stubQuery{search: func(path string) ([]*dpkg.OwnedFile, error) {
		searches++
		assert.Contains(t, []string{first, second}, path)
		return []*dpkg.OwnedFile{{Package: pkg, File: path}}, nil
	}}
	libs := []*Library{{Name: "one", Path: alias}, {Name: "alias", Path: first}, {Name: "missing"}, {Name: "two", Path: second}}
	f := NewFinder(DI{Query: q, Ldd: stubLdd(func(path string) (*LinkInfo, error) {
		lddCalls++
		assert.Contains(t, []string{"/bin/one", "/bin/two"}, path)
		return &LinkInfo{Libs: libs}, nil
	})})
	for _, path := range []string{"/bin/one", "/bin/one", "/bin/two"} {
		result, err := f.TraceDepsForExecutable(path)
		require.NoError(t, err)
		assert.Equal(t, []string{"missing"}, result.BrokenLibs)
		require.Len(t, result.Dependencies, 3)
		for i, lib := range []*Library{libs[0], libs[1], libs[3]} {
			assert.Same(t, lib, result.Dependencies[i].Library)
			assert.Same(t, pkg, result.Dependencies[i].Package)
		}
	}
	assert.Equal(t, 2, lddCalls)
	assert.Equal(t, 2, searches, "cache by resolved library path")
}

func TestFinderFailures(t *testing.T) {
	lib := filepath.Join(rootFSTempDir(t), "library")
	writeRootFSFile(t, lib, "library", 0644)
	failure := errors.New("query failed")
	for _, tc := range []struct {
		name, path        string
		lddErr, searchErr error
		want              string
	}{
		{name: "relative executable", path: "relative", want: "file path is not absolute"},
		{name: "ldd failure", lddErr: failure, want: failure.Error()},
		{name: "search failure", searchErr: failure, want: failure.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "/bin/app"
			}
			q := stubQuery{search: func(string) ([]*dpkg.OwnedFile, error) { return nil, tc.searchErr }}
			f := NewFinder(DI{Query: q, Ldd: stubLdd(func(string) (*LinkInfo, error) { return &LinkInfo{Libs: []*Library{{Path: lib}}}, tc.lddErr })})
			result, err := f.TraceDepsForExecutable(path)
			assert.Nil(t, result)
			require.ErrorContains(t, err, tc.want)
			if tc.lddErr != nil || tc.searchErr != nil {
				assert.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestFinderRetriesFailedCommands(t *testing.T) {
	lib := filepath.Join(rootFSTempDir(t), "library")
	writeRootFSFile(t, lib, "library", 0644)
	for _, stage := range []string{"ldd", "search"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("transient failure")
			calls := map[string]int{}
			fail := func(name string) bool { calls[name]++; return name == stage && calls[name] == 1 }
			pkg := testPackage(t, "pkg", "", "", "installed")
			q := stubQuery{search: func(string) ([]*dpkg.OwnedFile, error) {
				if fail("search") {
					return nil, failure
				}
				return []*dpkg.OwnedFile{{Package: pkg, File: lib}}, nil
			}}
			f := NewFinder(DI{Query: q, Ldd: stubLdd(func(string) (*LinkInfo, error) {
				if fail("ldd") {
					return nil, failure
				}
				return &LinkInfo{Libs: []*Library{{Path: lib}}}, nil
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
	library := &Library{Name: "library.so", Path: lib}
	q := stubQuery{search: func(path string) ([]*dpkg.OwnedFile, error) {
		assert.Equal(t, lib, path)
		return nil, nil
	}}
	f := NewFinder(DI{Query: q, Ldd: stubLdd(func(string) (*LinkInfo, error) { return &LinkInfo{Libs: []*Library{library}}, nil })})
	result, err := f.TraceDepsForExecutable("/bin/app")
	require.NoError(t, err)
	assert.Empty(t, result.BrokenLibs)
	require.Len(t, result.Dependencies, 1)
	assert.Same(t, library, result.Dependencies[0].Library)
	assert.Nil(t, result.Dependencies[0].Package)
}
