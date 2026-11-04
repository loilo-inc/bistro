package dpkg

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFixture(t *testing.T, baseDir, name, content string) {
	t.Helper()
	path := filepath.Join(baseDir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func newTestPackage(baseDir string, fields ...Field) *Package {
	p := &Package{baseDir: baseDir, fieldsMap: make(map[string]*Field, len(fields))}
	for i := range fields {
		field := &fields[i]
		p.fields = append(p.fields, field)
		p.fieldsMap[field.Key] = field
	}
	return p
}

func TestPackageNameWithArch(t *testing.T) {
	for _, tc := range []struct {
		name, arch, multiArch, want string
	}{
		{"same arm64", "arm64", "same", "example:arm64"},
		{"same amd64", "amd64", "same", "example:amd64"},
		{"all", "all", "", "example"},
		{"foreign all", "all", "foreign", "example"},
		{"foreign", "arm64", "foreign", "example"},
		{"allowed", "arm64", "allowed", "example"},
		{"no", "arm64", "no", "example"},
		{"unspecified", "arm64", "", "example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPackage("", Field{Key: "Package", Value: "example"}, Field{Key: "Architecture", Value: tc.arch}, Field{Key: "Multi-Arch", Value: tc.multiArch})
			assert.Equal(t, tc.want, p.NameWithArch())
		})
	}
}

func TestPackageStatus(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Status
	}{
		{"install ok installed", Status{"install", "ok", "installed"}},
		{"hold ok installed", Status{"hold", "ok", "installed"}},
		{"deinstall ok config-files", Status{"deinstall", "ok", "config-files"}},
		{"install reinstreq half-installed", Status{"install", "reinstreq", "half-installed"}},
		{"", Status{}},
		{"install ok", Status{}},
		{"install ok installed extra", Status{}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			p := newTestPackage("", Field{Key: "Status", Value: tc.input})
			assert.Equal(t, tc.want, p.Status())
		})
	}
}

func TestPackageInfoFiles(t *testing.T) {
	for _, multiArch := range []string{"", "same"} {
		t.Run("multiarch="+multiArch, func(t *testing.T) {
			baseDir := t.TempDir()
			p := newTestPackage(baseDir, Field{Key: "Package", Value: "example"}, Field{Key: "Architecture", Value: "arm64"}, Field{Key: "Multi-Arch", Value: multiArch})
			name := "example"
			if multiArch == "same" {
				name = "example:arm64"
			}
			writeFixture(t, baseDir, "info/"+name+".list", "/.\n/usr\n\n/usr/bin/example\n/etc/example.conf")
			writeFixture(t, baseDir, "info/"+name+".md5sums", "d41d8cd98f00b204e9800998ecf8427e  usr/bin/example\n\ne2fc714c4727ee9395f324cd2e7f331f  etc/example.conf")
			files, err := p.ListFiles()
			require.NoError(t, err)
			assert.Equal(t, rootfs.MustPaths("/.", "/usr", "/usr/bin/example", "/etc/example.conf"), files)
			sums, err := p.Md5Sums()
			require.NoError(t, err)
			assert.Equal(t, []Checksum{
				{Sum: "d41d8cd98f00b204e9800998ecf8427e", File: "usr/bin/example"},
				{Sum: "e2fc714c4727ee9395f324cd2e7f331f", File: "etc/example.conf"},
			}, sums.Entries)
			assert.Equal(t, "d41d8cd98f00b204e9800998ecf8427e  usr/bin/example\ne2fc714c4727ee9395f324cd2e7f331f  etc/example.conf\n", sums.String())
		})
	}
}

func TestPackageMissingInfoFiles(t *testing.T) {
	p := newTestPackage(t.TempDir(), Field{Key: "Package", Value: "example"})
	files, err := p.ListFiles()
	require.NoError(t, err)
	assert.Empty(t, files)
	sums, err := p.Md5Sums()
	require.NoError(t, err)
	assert.Empty(t, sums.Entries)
	assert.Empty(t, sums.String())
}

func TestPackageMd5SumsPreservesPathWhitespace(t *testing.T) {
	baseDir := t.TempDir()
	p := newTestPackage(baseDir, Field{Key: "Package", Value: "example"})
	const content = "d41d8cd98f00b204e9800998ecf8427e  usr/share/example/file with spaces\n" +
		"e2fc714c4727ee9395f324cd2e7f331f  usr/share/example/two  spaces\n" +
		"900150983cd24fb0d6963f7d28e17f72  usr/share/example/tab\tname\n"
	writeFixture(t, baseDir, "info/example.md5sums", content)
	sums, err := p.Md5Sums()
	require.NoError(t, err)
	assert.Equal(t, []Checksum{
		{Sum: "d41d8cd98f00b204e9800998ecf8427e", File: "usr/share/example/file with spaces"},
		{Sum: "e2fc714c4727ee9395f324cd2e7f331f", File: "usr/share/example/two  spaces"},
		{Sum: "900150983cd24fb0d6963f7d28e17f72", File: "usr/share/example/tab\tname"},
	}, sums.Entries)
	assert.Equal(t, content, sums.String())
}

func TestPackageInfoReadErrors(t *testing.T) {
	baseDir := t.TempDir()
	p := newTestPackage(baseDir, Field{Key: "Package", Value: "example"})
	// A directory at the file path reliably causes a read error, even as root.
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "info/example.list"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "info/example.md5sums"), 0755))
	_, err := p.ListFiles()
	requireReadDirError(t, err, filepath.Join(baseDir, "info/example.list"))
	_, err = p.Md5Sums()
	requireReadDirError(t, err, filepath.Join(baseDir, "info/example.md5sums"))
}

func requireReadDirError(t *testing.T, err error, path string) {
	t.Helper()
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	assert.Equal(t, "read", pathErr.Op)
	assert.Equal(t, path, pathErr.Path)
	assert.ErrorIs(t, err, syscall.EISDIR)
}
