package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnpackCommand(t *testing.T) {
	for _, flag := range []string{"", "-d", "--dest"} {
		t.Run("destination="+flag, func(t *testing.T) {
			dir := t.TempDir()
			for i, name := range []string{"first.tar", "second.tar"} {
				var buf bytes.Buffer
				archiver := rootfs.NewArchiver(fstest.MapFS{}, &buf)
				content := []byte("long original content")
				if i == 1 {
					content = []byte("new")
				}
				require.NoError(t, archiver.Add(rootfs.MustPath("/nested/file"), content, 0751))
				if i == 0 {
					require.NoError(t, archiver.Add(rootfs.MustPath("/first-only"), []byte("retained"), 0644))
				}
				require.NoError(t, archiver.Close())
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0600))
			}

			query := &failingLoadQuery{err: errors.New("unpack must not load dpkg")}
			resolver := &fixtureResolver{t: t, loadErr: errors.New("unpack must not load ld cache")}
			application := newFixtureRunner(t, fstest.MapFS{}, resolver)
			application.cwd = dir
			application.di.Query = query
			args := []string{"bistro", "unpack", "first.tar", "second.tar"}
			dest := dir
			if flag != "" {
				// Match the Dockerfile's flags-after-archive invocation.
				args = append(args, flag, "output")
				dest = filepath.Join(dir, "output")
			}
			require.NoError(t, application.command().Run(context.Background(), args))
			assert.Zero(t, query.loads)
			assert.Zero(t, resolver.loads)
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
