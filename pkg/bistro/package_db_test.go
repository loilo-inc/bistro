package bistro

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/internal/testfs"
	"github.com/loilo-inc/bistro/pkg/internal/teststub"
	"github.com/stretchr/testify/require"
)

type packageDBFixture struct {
	baseDir string
	fs      fs.FS
	query   *teststub.Query
}

func newPackageDBFixture(t *testing.T, baseDir string) *packageDBFixture {
	t.Helper()
	fss := testfs.Mount(os.DirFS("/"), "var/lib/dpkg", os.DirFS(baseDir))
	database := dpkg.NewQuery(fss)
	require.NoError(t, database.Load())
	query := teststub.NewQuery(t)
	query.GetFunc = database.Get
	query.LookUpFunc = database.LookUp
	query.ListFunc = database.List
	return &packageDBFixture{baseDir: baseDir, fs: fss, query: query}
}

// Build a real dpkg database, preserving the package pointers used by callers.
func packageFilesFixture(t *testing.T, packages []*dpkg.Package, files map[string][]string) *packageDBFixture {
	t.Helper()
	baseDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(baseDir, "info"), 0755))
	var status strings.Builder
	for _, pkg := range packages {
		status.WriteString(pkg.String())
		status.WriteString("\n")
		name := pkg.NameWithArch()
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info", name+".list"), []byte(strings.Join(files[name], "\n")), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "info", name+".md5sums"), []byte("d41d8cd98f00b204e9800998ecf8427e  usr/share/"+name+"\n"), 0644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "status"), []byte(status.String()), 0644))
	d := newPackageDBFixture(t, baseDir)
	for _, pkg := range packages {
		loaded, err := d.query.Get(pkg.Name())
		require.NoError(t, err)
		*pkg = *loaded
	}
	d.query.GetFunc = func(name string) (*dpkg.Package, error) {
		for _, pkg := range packages {
			if pkg.NameWithArch() == name || pkg.Name() == name {
				return pkg, nil
			}
		}
		return nil, fmt.Errorf("package not found: %s", name)
	}
	return d
}

func testPackage(t *testing.T, binaryName, version, arch, status string) *dpkg.Package {
	t.Helper()
	name, qualifier, qualified := strings.Cut(binaryName, ":")
	if arch == "" {
		arch = "all"
		if qualified {
			arch = qualifier
		}
	}
	if status == "" {
		status = dpkg.StatusInstalled
	}
	content := fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: %s\nStatus: install ok %s\n", name, version, arch, status)
	if qualified {
		content += "Multi-Arch: same\n"
	}
	packages, err := dpkg.ParseStatus(content)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	return packages[0]
}
