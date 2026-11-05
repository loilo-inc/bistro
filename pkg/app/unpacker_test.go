package app

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/loilo-inc/bistro/pkg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnpackCommand(t *testing.T) {
	testutil.RequireRoot(t)
	for _, flag := range []string{"", "-d", "--dest"} {
		t.Run("destination="+flag, func(t *testing.T) {
			dir := t.TempDir()
			for i, name := range []string{"first.tar", "second.tar"} {
				var buf bytes.Buffer
				writer := tar.NewWriter(&buf)
				content := []byte("long original content")
				if i == 1 {
					content = []byte("new")
				}
				require.NoError(t, writer.WriteHeader(&tar.Header{Name: "nested/file", Typeflag: tar.TypeReg, Mode: 0751, Size: int64(len(content))}))
				_, err := writer.Write(content)
				require.NoError(t, err)
				if i == 0 {
					require.NoError(t, writer.WriteHeader(&tar.Header{Name: "first-only", Typeflag: tar.TypeReg, Mode: 0644, Size: 8}))
					_, err := writer.Write([]byte("retained"))
					require.NoError(t, err)
				}
				require.NoError(t, writer.Close())
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0600))
			}

			fixture := newAppFixture(t)
			fixture.query.LoadErr = errors.New("unpack must not load dpkg")
			fixture.resolver.LoadErr = errors.New("unpack must not load ld cache")
			fixture.input.Cwd = dir
			args := []string{"unpack", "first.tar", "second.tar"}
			dest := dir
			if flag != "" {
				// Match the Dockerfile's flags-after-archive invocation.
				args = append(args, flag, "output")
				dest = filepath.Join(dir, "output")
			}
			require.NoError(t, fixture.run(args...))
			assert.Zero(t, fixture.query.Loads)
			assert.Zero(t, fixture.resolver.Loads)
			content, err := os.ReadFile(filepath.Join(dest, "nested", "file"))
			require.NoError(t, err)
			assert.Equal(t, "new", string(content))
			info, err := os.Stat(filepath.Join(dest, "nested", "file"))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0751), info.Mode().Perm())
			content, err = os.ReadFile(filepath.Join(dest, "first-only"))
			require.NoError(t, err)
			assert.Equal(t, "retained", string(content))
		})
	}
}
