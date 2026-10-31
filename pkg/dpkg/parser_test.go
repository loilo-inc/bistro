package dpkg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStatus(t *testing.T) {
	const input = "\nPackage: example\nStatus: install ok installed\nArchitecture: arm64\nVersion: 1:2.0-3\nMulti-Arch: same\nDescription: short description\n continuation: with colon\n .\n\ttab continuation\nConffiles:\n /etc/example.conf abc123\nX-Custom-Field: value\n\n\nPackage: other\nStatus: deinstall ok config-files\nArchitecture: all\nVersion: 4.0"
	pkgs, err := ParseStatus(input)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	assert.Equal(t, "example", pkgs[0].Name())
	assert.Equal(t, "arm64", pkgs[0].Architecture())
	assert.Equal(t, "1:2.0-3", pkgs[0].Version())
	assert.Equal(t, "short description\n continuation: with colon\n .\n\ttab continuation", pkgs[0].field("Description"))
	assert.Equal(t, "\n /etc/example.conf abc123", pkgs[0].field("Conffiles"))
	assert.Equal(t, "value", pkgs[0].field("X-Custom-Field"))
	assert.Equal(t, "other", pkgs[1].Name())
	assert.NotContains(t, pkgs[1].fieldsMap, "Description")

	var keys []string
	for _, field := range pkgs[0].fields {
		keys = append(keys, field.Key)
	}
	assert.Equal(t, []string{"Package", "Status", "Architecture", "Version", "Multi-Arch", "Description", "Conffiles", "X-Custom-Field"}, keys)

	// Serialization must preserve field order and values.
	roundTrip, err := ParseStatus(pkgs[0].String())
	require.NoError(t, err)
	require.Len(t, roundTrip, 1)
	assert.Equal(t, pkgs[0].fields, roundTrip[0].fields)
}

func TestParseStatusEmpty(t *testing.T) {
	for _, input := range []string{"", "\n", "\n\n"} {
		pkgs, err := ParseStatus(input)
		require.NoError(t, err)
		assert.Empty(t, pkgs)
	}
}

func TestParseStatusInvalidLine(t *testing.T) {
	pkgs, err := ParseStatus("Package: example\ninvalid line\n")
	require.ErrorContains(t, err, "invalid line in status file: invalid line")
	assert.Nil(t, pkgs)
}
