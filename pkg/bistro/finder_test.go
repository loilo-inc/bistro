package bistro

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	lddCalls, searches, queries := 0, 0, 0
	pkg := &Package{Package: "shared:arm64", Status: "unpacked"}
	d := stubDpkg{
		search: func(path string) ([]*OwnedFile, error) {
			searches++
			assert.Contains(t, []string{first, second}, path)
			return []*OwnedFile{{Package: pkg.Package, File: path}}, nil
		},
		listPackages: func(names ...string) ([]*Package, error) {
			queries++
			assert.Equal(t, []string{pkg.Package}, names)
			return []*Package{pkg}, nil
		},
	}
	libs := []*Library{{Name: "one", Path: alias}, {Name: "alias", Path: first}, {Name: "missing"}, {Name: "two", Path: second}}
	f := NewFinder(d, stubLdd(func(path string) (*LinkInfo, error) {
		lddCalls++
		assert.Contains(t, []string{"/bin/one", "/bin/two"}, path)
		return &LinkInfo{Libs: libs}, nil
	}))
	for _, path := range []string{"/bin/one", "/bin/one", "/bin/two"} {
		result, err := f.TraceDepsForExecutable(path)
		require.NoError(t, err)
		assert.Equal(t, []string{"missing"}, result.BrokenLibs)
		require.Len(t, result.Dependencies, 3)
		for i, lib := range []*Library{libs[0], libs[1], libs[3]} {
			assert.Equal(t, lib, result.Dependencies[i].Library)
			assert.Same(t, pkg, result.Dependencies[i].Package)
		}
	}
	assert.Equal(t, 2, lddCalls)
	assert.Equal(t, 2, searches, "cache by resolved library path")
	assert.Equal(t, 1, queries, "cache by owning package")
}

func TestFinderFailures(t *testing.T) {
	dir := rootFSTempDir(t)
	lib := filepath.Join(dir, "library")
	writeRootFSFile(t, lib, "library", 0644)
	failure := errors.New("command failed")
	for _, tt := range []struct {
		name                        string
		path                        string
		lddErr, searchErr, queryErr error
		owners                      bool
		want                        string
	}{
		{name: "relative executable", path: "relative", want: "file path is not absolute"},
		{name: "ldd failure", lddErr: failure, want: failure.Error()},
		{name: "search failure", searchErr: failure, want: failure.Error()},
		{name: "unowned library", want: "library not found in any package"},
		{name: "query failure", owners: true, queryErr: failure, want: failure.Error()},
		{name: "missing package", owners: true, want: "package not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path == "" {
				path = "/bin/app"
			}
			d := stubDpkg{
				search: func(string) ([]*OwnedFile, error) {
					if tt.owners {
						return []*OwnedFile{{Package: "pkg"}}, tt.searchErr
					}
					return nil, tt.searchErr
				},
				listPackages: func(...string) ([]*Package, error) { return nil, tt.queryErr },
			}
			f := NewFinder(d, stubLdd(func(string) (*LinkInfo, error) { return &LinkInfo{Libs: []*Library{{Path: lib}}}, tt.lddErr }))
			result, err := f.TraceDepsForExecutable(path)
			assert.Nil(t, result)
			require.ErrorContains(t, err, tt.want)
			if tt.lddErr != nil || tt.searchErr != nil || tt.queryErr != nil {
				assert.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestFinderRetriesFailedCommands(t *testing.T) {
	dir := rootFSTempDir(t)
	lib := filepath.Join(dir, "library")
	writeRootFSFile(t, lib, "library", 0644)
	for _, stage := range []string{"ldd", "search", "query"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("transient failure")
			calls := map[string]int{}
			fail := func(name string) bool { calls[name]++; return name == stage && calls[name] == 1 }
			d := stubDpkg{
				search: func(string) ([]*OwnedFile, error) {
					if fail("search") {
						return nil, failure
					}
					return []*OwnedFile{{Package: "pkg"}}, nil
				},
				listPackages: func(...string) ([]*Package, error) {
					if fail("query") {
						return nil, failure
					}
					return []*Package{{Package: "pkg"}}, nil
				},
			}
			f := NewFinder(d, stubLdd(func(string) (*LinkInfo, error) {
				if fail("ldd") {
					return nil, failure
				}
				return &LinkInfo{Libs: []*Library{{Path: lib}}}, nil
			}))
			_, err := f.TraceDepsForExecutable("/bin/app")
			require.ErrorIs(t, err, failure)
			result, err := f.TraceDepsForExecutable("/bin/app")
			require.NoError(t, err)
			require.Len(t, result.Dependencies, 1)
			assert.Equal(t, 2, calls[stage])
		})
	}
}

func TestRealpath(t *testing.T) {
	dir := rootFSTempDir(t)
	file := filepath.Join(dir, "file")
	writeRootFSFile(t, file, "content", 0644)
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink("file", link))
	for _, path := range []string{file, link} {
		got, err := realpath(path)
		require.NoError(t, err)
		assert.Equal(t, file, got)
	}
	for _, path := range []string{"relative", dir, filepath.Join(dir, "absent")} {
		_, err := realpath(path)
		require.Error(t, err)
		_, err = NewFinder(nil, nil).(*finder).CollectPackage(path)
		require.Error(t, err)
	}
}
