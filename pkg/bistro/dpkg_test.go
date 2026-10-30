package bistro

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readDpkgFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "dpkg", name))
	require.NoError(t, err)
	return data
}

type dpkgTestExecer func(string, ...string) ([]byte, error)

func (f dpkgTestExecer) Output(name string, args ...string) ([]byte, error) {
	return f(name, args...)
}

func TestDpkgListPackages(t *testing.T) {
	for _, tt := range []struct {
		name   string
		groups []string
		args   []string
	}{
		{name: "all packages"},
		{name: "selected packages", groups: []string{"libc6:amd64", "libc6:i386", "example"}, args: []string{"libc6:amd64", "libc6:i386", "example"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			d := NewDpkg(dpkgTestExecer(func(name string, args ...string) ([]byte, error) {
				calls++
				assert.Equal(t, "dpkg-query", name)
				wantArgs := append([]string{"-W", "-f=${binary:Package}\t${Version}\t${Architecture}\t${db:Status-Status}\n"}, tt.args...)
				assert.Equal(t, wantArgs, args)
				return readDpkgFixture(t, "packages.txt"), nil
			}))
			got, err := d.ListPackages(tt.groups...)
			require.NoError(t, err)
			assert.Equal(t, []*Package{
				{Package: "libc6:amd64", Version: "2.36-9+deb12u1", Architecture: "amd64", Status: "installed"},
				{Package: "libc6:i386", Version: "2.36-9+deb12u1", Architecture: "i386", Status: "installed"},
				{Package: "example", Version: "1:2.0-3", Architecture: "all", Status: "config-files"},
			}, got)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestDpkgListFiles(t *testing.T) {
	d := NewDpkg(dpkgTestExecer(func(name string, args ...string) ([]byte, error) {
		assert.Equal(t, "dpkg-query", name)
		assert.Equal(t, []string{"-W", "-f=${db-fsys:Files}", "example:amd64"}, args)
		// db-fsys:Files prefixes each filesystem entry with one space.
		return readDpkgFixture(t, "files.txt"), nil
	}))
	got, err := d.ListFiles("example:amd64")
	require.NoError(t, err)
	assert.Equal(t, []string{"/.", "/usr", "/usr/share/example/file with spaces", "/usr/share/example/trailing ", "/usr/share/example/tab\t"}, got)
}

func TestDpkgSearch(t *testing.T) {
	d := NewDpkg(dpkgTestExecer(func(name string, args ...string) ([]byte, error) {
		assert.Equal(t, "dpkg-query", name)
		assert.Equal(t, []string{"-S", "/usr/share/example/*"}, args)
		return readDpkgFixture(t, "search.txt"), nil
	}))
	got, err := d.Search("/usr/share/example/*")
	require.NoError(t, err)
	assert.Equal(t, []*OwnedFile{
		{Package: "libc6:amd64", File: "/usr/share/example/copyright"},
		{Package: "libc6:i386", File: "/usr/share/example/copyright"},
		{Package: "example", File: "/usr/share/example/file: with spaces"},
	}, got)
}

func TestDpkgSearchExitErrors(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("BISTRO_TEST_HELPER", "1")
	for _, tt := range []struct {
		name    string
		mode    string
		noMatch bool
	}{
		{name: "no matching path", mode: "dpkg-not-found", noMatch: true},
		{name: "other exit one", mode: "dpkg-error"},
		{name: "fatal error", mode: "failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			libraryPath := filepath.Join(rootFSTempDir(t), "library.so")
			writeRootFSFile(t, libraryPath, "library", 0644)
			_, commandErr := NewExecer().Output(executable, "-test.run=^TestExecerHelperProcess$", "--", tt.mode)
			var exitErr *exec.ExitError
			require.ErrorAs(t, commandErr, &exitErr)
			d := NewDpkg(dpkgTestExecer(func(name string, args ...string) ([]byte, error) {
				assert.Equal(t, "dpkg-query", name)
				assert.Equal(t, []string{"-S", libraryPath}, args)
				return nil, commandErr
			}))
			got, err := d.Search(libraryPath)
			assert.Nil(t, got)
			if tt.noMatch {
				require.NoError(t, err)
				library := &Library{Name: "library.so", Path: libraryPath}
				f := NewFinder(d, stubLdd(func(string) (*LinkInfo, error) {
					return &LinkInfo{Libs: []*Library{library}}, nil
				}))
				result, err := f.TraceDepsForExecutable("/bin/app")
				require.NoError(t, err)
				require.Len(t, result.Dependencies, 1)
				assert.Same(t, library, result.Dependencies[0].Library)
				assert.Nil(t, result.Dependencies[0].Package)
				assert.Empty(t, result.BrokenLibs)
			} else {
				assert.ErrorIs(t, err, commandErr)
			}
		})
	}
}

func TestDpkgMetadata(t *testing.T) {
	for _, tt := range []struct {
		name    string
		call    func(Dpkg, string) ([]byte, error)
		args    []string
		fixture string
	}{
		{
			name: "status", call: Dpkg.PackageStatus,
			args:    []string{"-s", "example:amd64"},
			fixture: "status.txt",
		},
		{
			name: "md5sums", call: Dpkg.PackageMd5Sums,
			args:    []string{"--control-show", "example:amd64", "md5sums"},
			fixture: "md5sums.txt",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := readDpkgFixture(t, tt.fixture)
			d := NewDpkg(dpkgTestExecer(func(name string, args ...string) ([]byte, error) {
				assert.Equal(t, "dpkg-query", name)
				assert.Equal(t, tt.args, args)
				return data, nil
			}))
			got, err := tt.call(d, "example:amd64")
			require.NoError(t, err)
			assert.Equal(t, data, got)
		})
	}
}

func TestDpkgCommandErrors(t *testing.T) {
	commandErr := errors.New("dpkg-query failed")
	d := NewDpkg(dpkgTestExecer(func(string, ...string) ([]byte, error) {
		return nil, commandErr
	}))
	t.Run("list packages", func(t *testing.T) {
		got, err := d.ListPackages()
		assert.ErrorIs(t, err, commandErr)
		assert.Nil(t, got)
	})
	t.Run("list files", func(t *testing.T) {
		got, err := d.ListFiles("example")
		assert.ErrorIs(t, err, commandErr)
		assert.Nil(t, got)
	})
	t.Run("search", func(t *testing.T) {
		got, err := d.Search("/usr/bin/example")
		assert.ErrorIs(t, err, commandErr)
		assert.Nil(t, got)
	})
	t.Run("status", func(t *testing.T) {
		got, err := d.PackageStatus("example")
		assert.ErrorIs(t, err, commandErr)
		assert.Nil(t, got)
	})
	t.Run("missing md5sums is an error", func(t *testing.T) {
		got, err := d.PackageMd5Sums("example")
		assert.ErrorIs(t, err, commandErr)
		assert.Nil(t, got)
	})
}

func TestDpkgEmptyOutput(t *testing.T) {
	d := NewDpkg(dpkgTestExecer(func(string, ...string) ([]byte, error) {
		return readDpkgFixture(t, "blank_lines.txt"), nil
	}))
	packages, err := d.ListPackages()
	require.NoError(t, err)
	assert.Empty(t, packages)
	files, err := d.ListFiles("example")
	require.NoError(t, err)
	assert.Empty(t, files)
	owners, err := d.Search("/usr/bin/example")
	require.NoError(t, err)
	assert.Empty(t, owners)
}

func TestDpkgListPackagesSkipsIncompleteRows(t *testing.T) {
	d := NewDpkg(dpkgTestExecer(func(string, ...string) ([]byte, error) {
		return []byte("missing-fields\nname\tversion\tarch\nvalid:arm64\t1.0\tarm64\tinstalled\n"), nil
	}))
	got, err := d.ListPackages()
	require.NoError(t, err)
	assert.Equal(t, []*Package{{Package: "valid:arm64", Version: "1.0", Architecture: "arm64", Status: "installed"}}, got)
}

func TestDiffPackages(t *testing.T) {
	old := &Package{Package: "libc6:amd64", Version: "1", Architecture: "amd64", Status: "installed"}
	updated := &Package{Package: "libc6:amd64", Version: "2", Architecture: "amd64", Status: "installed"}
	otherArch := &Package{Package: "libc6:i386", Version: "1", Architecture: "i386", Status: "installed"}
	added := &Package{Package: "added", Version: "1"}
	removed := &Package{Package: "removed", Version: "1"}
	for _, tt := range []struct {
		name          string
		before, after []*Package
		want          PackageDiff
	}{
		{name: "empty"},
		{name: "all added", after: []*Package{added, otherArch}, want: PackageDiff{Added: []*Package{added, otherArch}}},
		{name: "all removed", before: []*Package{removed, otherArch}, want: PackageDiff{Removed: []*Package{removed, otherArch}}},
		{name: "unchanged with different order", before: []*Package{old, otherArch}, after: []*Package{otherArch, old}},
		{
			name:   "added removed and updated with independent architectures",
			before: []*Package{old, otherArch, removed}, after: []*Package{updated, otherArch, added},
			want: PackageDiff{Added: []*Package{added}, Removed: []*Package{removed}, Changed: []*Package{updated}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiffPackages(tt.before, tt.after)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want.Added, got.Added)
			assert.ElementsMatch(t, tt.want.Removed, got.Removed)
			assert.ElementsMatch(t, tt.want.Changed, got.Changed)
		})
	}
}

func TestParseSearchData(t *testing.T) {
	for _, tt := range []struct {
		fixture string
		want    []*OwnedFile
	}{
		{fixture: "empty.txt"},
		{fixture: "blank_lines.txt"},
		{fixture: "search_invalid.txt"},
		{fixture: "search_diversions_only.txt"},
		{
			fixture: "search_single.txt",
			want:    []*OwnedFile{{Package: "example", File: "/usr/share/example/file: with spaces"}},
		},
		{
			fixture: "search_multiple.txt",
			want: []*OwnedFile{
				{Package: "libc6:amd64", File: "/usr/share/doc/libc6/copyright"},
				{Package: "libc6:i386", File: "/usr/share/doc/libc6/copyright"},
			},
		},
		{
			fixture: "search_data.txt",
			want: []*OwnedFile{
				{Package: "libc6:amd64", File: "/usr/share/doc/libc6/copyright"},
				{Package: "libc6:i386", File: "/usr/share/doc/libc6/copyright"},
				{Package: "example", File: "/usr/share/example/file: with spaces"},
			},
		},
		{
			fixture: "search_diversions.txt",
			want: []*OwnedFile{
				{Package: "example:amd64", File: "/usr/bin/tool"},
				{Package: "local-tool", File: "/usr/bin/local-tool"},
				{Package: "diversion-tool", File: "/usr/bin/diversion-tool"},
			},
		},
		{
			fixture: "search_whitespace.txt",
			want: []*OwnedFile{
				{Package: "example", File: "/usr/share/example/file "},
				{Package: "example", File: "/usr/share/example/file\t"},
				{Package: "example", File: "/usr/share/example/file: with, punctuation"},
			},
		},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			assert.Equal(t, tt.want, parseSearchData(readDpkgFixture(t, tt.fixture)))
		})
	}
}
