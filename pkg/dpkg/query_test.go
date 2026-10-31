package dpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryBeforeLoad(t *testing.T) {
	q := NewQuery()
	_, err := q.Get("example")
	require.ErrorContains(t, err, "packages not loaded")
	_, err = q.List()
	require.ErrorContains(t, err, "packages not loaded")
	_, err = q.Search("/usr/bin/example")
	require.ErrorContains(t, err, "packages not loaded")
}

func TestQueryLoad(t *testing.T) {
	baseDir := t.TempDir()
	writeFixture(t, baseDir, "status", "Package: example\nArchitecture: arm64\nMulti-Arch: same\nStatus: install ok installed\nVersion: 1.0\n\nPackage: other\nArchitecture: all\nStatus: install ok installed\nVersion: 2.0\n")
	writeFixture(t, baseDir, "info/example:arm64.list", "/usr/bin/example\n/usr/share/common\n")
	writeFixture(t, baseDir, "info/other.list", "/usr/bin/other\n/usr/share/common\n")
	q := &query{baseDir: baseDir}
	require.NoError(t, q.Load())
	pkg, err := q.Get("example")
	require.NoError(t, err)
	assert.Equal(t, "1.0", pkg.Version())
	assert.Equal(t, StatusInstalled, pkg.Status().Current)
	other, err := q.Get("other")
	require.NoError(t, err)
	list, err := q.List()
	require.NoError(t, err)
	assert.ElementsMatch(t, []*Package{pkg, other}, list)
	_, err = q.Get("missing")
	require.ErrorContains(t, err, "package not found: missing")

	for _, tc := range []struct {
		pattern string
		want    []*OwnedFile
	}{
		{"/usr/bin/example", []*OwnedFile{{Package: pkg, File: "/usr/bin/example"}}},
		{"/usr/bin/*", []*OwnedFile{{Package: pkg, File: "/usr/bin/example"}, {Package: other, File: "/usr/bin/other"}}},
		{"/usr/share/common", []*OwnedFile{{Package: pkg, File: "/usr/share/common"}, {Package: other, File: "/usr/share/common"}}},
		{"/missing", nil},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			found, err := q.Search(tc.pattern)
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, found)
		})
	}

	writeFixture(t, baseDir, "status", "Package: replacement\nArchitecture: all\n")
	require.NoError(t, q.Load())
	list, err = q.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "replacement", list[0].Name())
	_, err = q.Get("example")
	require.Error(t, err)
	_, err = q.Get("example:arm64")
	require.ErrorContains(t, err, "package not found: example:arm64")
}

func TestQueryLoadErrors(t *testing.T) {
	q := &query{baseDir: t.TempDir()}
	err := q.Load()
	require.ErrorIs(t, err, os.ErrNotExist)
	writeFixture(t, q.baseDir, "status", "Package: example\ninvalid line\n")
	require.ErrorContains(t, q.Load(), "invalid line in status file")
}

func TestQueryEmptyStatus(t *testing.T) {
	q := &query{baseDir: t.TempDir()}
	writeFixture(t, q.baseDir, "status", "")
	require.NoError(t, q.Load())
	list, err := q.List()
	require.NoError(t, err)
	assert.Empty(t, list)
	found, err := q.Search("/usr/bin/example")
	require.NoError(t, err)
	assert.Empty(t, found)
	_, err = q.Get("example")
	require.ErrorContains(t, err, "package not found: example")
}

func TestQuerySearchReadError(t *testing.T) {
	q := &query{baseDir: t.TempDir()}
	writeFixture(t, q.baseDir, "status", "Package: example\nArchitecture: all\nStatus: install ok installed\n")
	// A directory at the file path causes a read error other than not-exist.
	listPath := filepath.Join(q.baseDir, "info/example.list")
	require.NoError(t, os.MkdirAll(listPath, 0755))
	require.NoError(t, q.Load())
	_, err := q.Search("/usr/bin/example")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	assert.Equal(t, listPath, pathErr.Path)
}

func TestQueryAliases(t *testing.T) {
	for _, tc := range []struct{ arch, multiArch string }{
		{"arm64", "same"},
		{"amd64", "same"},
		{"arm64", "foreign"},
		{"arm64", "allowed"},
		{"arm64", ""},
		{"all", "foreign"},
	} {
		t.Run(tc.arch+"/"+tc.multiArch, func(t *testing.T) {
			baseDir := t.TempDir()
			writeFixture(t, baseDir, "status", fmt.Sprintf("Package: example\nArchitecture: %s\nMulti-Arch: %s\nStatus: install ok installed\n", tc.arch, tc.multiArch))
			infoName := "example"
			if tc.multiArch == "same" {
				infoName += ":" + tc.arch
			}
			writeFixture(t, baseDir, "info/"+infoName+".list", "/usr/bin/example\n")
			q := &query{baseDir: baseDir, arch: tc.arch}
			require.NoError(t, q.Load())
			pkg, err := q.Get("example")
			require.NoError(t, err)
			qualified, err := q.Get("example:" + tc.arch)
			require.NoError(t, err)
			assert.Same(t, pkg, qualified)
			list, err := q.List()
			require.NoError(t, err)
			require.Len(t, list, 1)
			assert.Same(t, pkg, list[0])
			found, err := q.Search("/usr/bin/example")
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Same(t, pkg, found[0].Package)
			assert.Equal(t, "/usr/bin/example", found[0].File)
		})
	}
}

func TestQueryMultiarch(t *testing.T) {
	for _, native := range []string{"amd64", "arm64"} {
		for _, arches := range [][]string{{"amd64", "arm64"}, {"arm64", "amd64"}} {
			t.Run(native+"/first="+arches[0], func(t *testing.T) {
				baseDir := t.TempDir()
				var status string
				for _, arch := range arches {
					status += fmt.Sprintf("Package: example\nArchitecture: %s\nMulti-Arch: same\nStatus: install ok installed\n\n", arch)
					writeFixture(t, baseDir, "info/example:"+arch+".list", "/usr/lib/"+arch+"/libexample.so\n/usr/share/example/copyright\n")
				}
				writeFixture(t, baseDir, "status", status)
				q := &query{baseDir: baseDir, arch: native}
				require.NoError(t, q.Load())
				selected, err := q.Get("example")
				require.NoError(t, err)
				assert.Equal(t, native, selected.Architecture())
				var packages []*Package
				var sharedOwners []*OwnedFile
				for _, arch := range arches {
					pkg, err := q.Get("example:" + arch)
					require.NoError(t, err)
					assert.Equal(t, arch, pkg.Architecture())
					if arch == native {
						assert.Same(t, selected, pkg)
					}
					packages = append(packages, pkg)
					sharedOwners = append(sharedOwners, &OwnedFile{Package: pkg, File: "/usr/share/example/copyright"})
					path := "/usr/lib/" + arch + "/libexample.so"
					found, err := q.Search(path)
					require.NoError(t, err)
					require.Len(t, found, 1)
					assert.Same(t, pkg, found[0].Package)
					assert.Equal(t, path, found[0].File)
				}
				list, err := q.List()
				require.NoError(t, err)
				assert.ElementsMatch(t, packages, list)
				found, err := q.Search("/usr/share/example/copyright")
				require.NoError(t, err)
				assert.ElementsMatch(t, sharedOwners, found)
			})
		}
	}
}

func TestQueryMultiarchFallback(t *testing.T) {
	for _, native := range []string{"amd64", "arm64"} {
		t.Run(native, func(t *testing.T) {
			baseDir := t.TempDir()
			foreign := "arm64"
			if native == "arm64" {
				foreign = "amd64"
			}
			writeFixture(t, baseDir, "status", fmt.Sprintf("Package: example\nArchitecture: %s\nMulti-Arch: same\n\nPackage: example\nArchitecture: i386\nMulti-Arch: same\n", foreign))
			q := &query{baseDir: baseDir, arch: native}
			require.NoError(t, q.Load())
			selected, err := q.Get("example")
			require.NoError(t, err)
			first, err := q.Get("example:" + foreign)
			require.NoError(t, err)
			assert.Same(t, first, selected, "use the first entry when no architecture matches")
		})
	}
}

func TestQuerySearchSkipsMissingLists(t *testing.T) {
	baseDir := t.TempDir()
	writeFixture(t, baseDir, "status", "Package: held\nStatus: hold ok not-installed\n\nPackage: missing\nArchitecture: all\nStatus: install ok installed\n\nPackage: example\nArchitecture: all\nStatus: install ok installed\n")
	// Both uninstalled and installed packages may have no .list file.
	writeFixture(t, baseDir, "info/example.list", "/usr/bin/example\n")
	q := NewQueryWithBaseDir(baseDir)
	require.NoError(t, q.Load())
	found, err := q.Search("/usr/bin/example")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "example", found[0].Package.Name())
}

func TestQuerySearchFindsUnconfiguredPackages(t *testing.T) {
	for _, status := range []string{"half-installed", "unpacked", "half-configured", "triggers-awaited", "triggers-pending"} {
		t.Run(status, func(t *testing.T) {
			baseDir := t.TempDir()
			writeFixture(t, baseDir, "status", fmt.Sprintf("Package: example\nArchitecture: all\nStatus: install ok %s\n", status))
			writeFixture(t, baseDir, "info/example.list", "/usr/bin/example\n")
			q := NewQueryWithBaseDir(baseDir)
			require.NoError(t, q.Load())
			found, err := q.Search("/usr/bin/example")
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, "example", found[0].Package.Name())
			assert.Equal(t, status, found[0].Package.Status().Current)
		})
	}
}
