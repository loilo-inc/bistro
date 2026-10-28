package bistro

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readUtilFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "util", name))
	require.NoError(t, err)
	return data
}

func TestParseLines(t *testing.T) {
	for _, tt := range []struct {
		fixture string
		want    []string
	}{
		{fixture: "empty.txt"},
		{fixture: "blank_lines.txt"},
		{
			fixture: "lines.txt",
			want:    []string{"/usr", "/usr/file with spaces", "/usr/trailing ", "/usr/tab\t", " /two-leading-spaces", "\tleading-tab", "/last"},
		},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			assert.Equal(t, tt.want, parseLines(readUtilFixture(t, tt.fixture)))
		})
	}
}

func TestParseTabSplitData(t *testing.T) {
	for _, tt := range []struct {
		fixture string
		want    [][]string
	}{
		{fixture: "empty.txt"},
		{fixture: "blank_lines.txt"},
		{
			fixture: "tabs.txt",
			want: [][]string{
				{"libc6:amd64", "2.36-9", "amd64", "installed"},
				{"", "middle", "", "last", ""},
				{"single field"},
			},
		},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			assert.Equal(t, tt.want, parseTabSplitData(readUtilFixture(t, tt.fixture)))
		})
	}
}
