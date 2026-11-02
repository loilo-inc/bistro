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
	_, err = q.LookUp("/usr/bin/example")
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
		path string
		want *Package
	}{
		{"/usr/bin/example", pkg},
		{"/usr/bin/other", other},
		{"/usr/bin/*", nil}, // LookUp performs literal, exact-path matching.
		{"/missing", nil},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for range 2 {
				found, err := q.LookUp(tc.path)
				require.NoError(t, err)
				assert.Same(t, tc.want, found)
			}
		})
	}

	writeFixture(t, baseDir, "status", "Package: replacement\nArchitecture: all\n")
	writeFixture(t, baseDir, "info/replacement.list", "/usr/bin/replacement\n")
	require.NoError(t, q.Load())
	list, err = q.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "replacement", list[0].Name())
	_, err = q.Get("example")
	require.EqualError(t, err, "package not found: example")
	_, err = q.Get("example:arm64")
	require.ErrorContains(t, err, "package not found: example:arm64")
	found, err := q.LookUp("/usr/bin/example")
	require.NoError(t, err)
	assert.Nil(t, found, "Load must invalidate ownership from the previous database")
	found, err = q.LookUp("/usr/bin/replacement")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Same(t, list[0], found)
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
	found, err := q.LookUp("/usr/bin/example")
	require.NoError(t, err)
	assert.Empty(t, found)
	_, err = q.Get("example")
	require.ErrorContains(t, err, "package not found: example")
}

func TestQueryLookUpReadError(t *testing.T) {
	q := &query{baseDir: t.TempDir()}
	writeFixture(t, q.baseDir, "status", "Package: example\nArchitecture: all\nStatus: install ok installed\n")
	// A directory at the file path causes a read error other than not-exist.
	listPath := filepath.Join(q.baseDir, "info/example.list")
	require.NoError(t, os.MkdirAll(listPath, 0755))
	require.NoError(t, q.Load())
	_, err := q.LookUp("/usr/bin/example")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	assert.Equal(t, listPath, pathErr.Path)
	_, err = q.LookUp("/usr/bin/example")
	require.ErrorAs(t, err, &pathErr, "an incomplete index must not hide read errors")
	require.NoError(t, os.Remove(listPath))
	writeFixture(t, q.baseDir, "info/example.list", "/usr/bin/example\n")
	found, err := q.LookUp("/usr/bin/example")
	require.NoError(t, err)
	require.NotNil(t, found, "retry building the index after the read error is resolved")
	assert.Equal(t, "example", found.Name())
}

func TestQueryLookUpRejectsRelativePaths(t *testing.T) {
	q := NewQueryWithBaseDir(t.TempDir())
	for _, path := range []string{"", "usr/bin/example", "./example"} {
		t.Run(path, func(t *testing.T) {
			_, err := q.LookUp(path)
			require.ErrorContains(t, err, "absolute path")
		})
	}
}

func TestQueryLookUpLiteralMetacharacters(t *testing.T) {
	baseDir := t.TempDir()
	writeFixture(t, baseDir, "status", "Package: example\nArchitecture: all\n")
	const path = "/usr/share/example/[data]*?"
	writeFixture(t, baseDir, "info/example.list", path+"\n")
	q := NewQueryWithBaseDir(baseDir)
	require.NoError(t, q.Load())
	want, err := q.Get("example")
	require.NoError(t, err)
	for range 2 {
		found, err := q.LookUp(path)
		require.NoError(t, err)
		assert.Same(t, want, found)
	}
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
			found, err := q.LookUp("/usr/bin/example")
			require.NoError(t, err)
			assert.Same(t, pkg, found)
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
				for _, arch := range arches {
					pkg, err := q.Get("example:" + arch)
					require.NoError(t, err)
					assert.Equal(t, arch, pkg.Architecture())
					if arch == native {
						assert.Same(t, selected, pkg)
					}
					packages = append(packages, pkg)
					path := "/usr/lib/" + arch + "/libexample.so"
					found, err := q.LookUp(path)
					require.NoError(t, err)
					assert.Same(t, pkg, found)
				}
				list, err := q.List()
				require.NoError(t, err)
				assert.ElementsMatch(t, packages, list)
				found, err := q.LookUp("/usr/share/example/copyright")
				require.NoError(t, err)
				assert.True(t, found == packages[0] || found == packages[1], "shared files may resolve to either owning package")
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

func TestQueryLookUpSkipsMissingLists(t *testing.T) {
	baseDir := t.TempDir()
	writeFixture(t, baseDir, "status", "Package: held\nStatus: hold ok not-installed\n\nPackage: missing\nArchitecture: all\nStatus: install ok installed\n\nPackage: example\nArchitecture: all\nStatus: install ok installed\n")
	// Both uninstalled and installed packages may have no .list file.
	writeFixture(t, baseDir, "info/example.list", "/usr/bin/example\n")
	q := NewQueryWithBaseDir(baseDir)
	require.NoError(t, q.Load())
	found, err := q.LookUp("/usr/bin/example")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "example", found.Name())
}

func TestQueryLookUpFindsUnconfiguredPackages(t *testing.T) {
	for _, status := range []string{"half-installed", "unpacked", "half-configured", "triggers-awaited", "triggers-pending"} {
		t.Run(status, func(t *testing.T) {
			baseDir := t.TempDir()
			writeFixture(t, baseDir, "status", fmt.Sprintf("Package: example\nArchitecture: all\nStatus: install ok %s\n", status))
			writeFixture(t, baseDir, "info/example.list", "/usr/bin/example\n")
			q := NewQueryWithBaseDir(baseDir)
			require.NoError(t, q.Load())
			found, err := q.LookUp("/usr/bin/example")
			require.NoError(t, err)
			require.NotNil(t, found)
			assert.Equal(t, "example", found.Name())
			assert.Equal(t, status, found.Status().Current)
		})
	}
}
