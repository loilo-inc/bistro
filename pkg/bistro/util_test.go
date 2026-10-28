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

func TestParseSearchLine(t *testing.T) {
	for _, tt := range []struct {
		fixture string
		want    []*OwnedFile
	}{
		{fixture: "empty.txt"},
		{fixture: "search_invalid.txt"},
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
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			assert.Equal(t, tt.want, parseSearchLine(string(readUtilFixture(t, tt.fixture))))
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
		{
			fixture: "search_data.txt",
			want: []*OwnedFile{
				{Package: "libc6:amd64", File: "/usr/share/doc/libc6/copyright"},
				{Package: "libc6:i386", File: "/usr/share/doc/libc6/copyright"},
				{Package: "example", File: "/usr/share/example/file: with spaces"},
			},
		},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			assert.Equal(t, tt.want, parseSearchData(readUtilFixture(t, tt.fixture)))
		})
	}
}
