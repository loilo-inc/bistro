package bistro

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reuse the Go test executable as a child process instead of depending on a
// shell or platform-specific system commands.
func TestExecerHelperProcess(t *testing.T) {
	if os.Getenv("BISTRO_TEST_HELPER") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "environment":
		fmt.Printf("%s|%s|%s|%s", os.Getenv("LC_ALL"), os.Getenv("LANGUAGE"), os.Getenv("LANG"), os.Getenv("BISTRO_TEST_INHERITED"))
	case "failure":
		fmt.Print("partial output")
		os.Exit(23)
	default:
		fmt.Print(os.Args[len(os.Args)-1])
	}
	os.Exit(0)
}

func TestExecerOutput(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("BISTRO_TEST_HELPER", "1")
	t.Setenv("BISTRO_TEST_INHERITED", "preserved")
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	t.Setenv("LANGUAGE", "ja")
	t.Setenv("LANG", "ja_JP.UTF-8")
	e := NewExecer()
	out, err := e.Output(executable, "-test.run=^TestExecerHelperProcess$", "--", "environment")
	require.NoError(t, err)
	assert.Equal(t, "C|C|C|preserved", string(out))
	literal := "a file with spaces; $(not-a-command) *"
	out, err = e.Output(executable, "-test.run=^TestExecerHelperProcess$", "--", literal)
	require.NoError(t, err)
	assert.Equal(t, literal, string(out), "arguments must be passed without shell expansion")
	out, err = e.Output(executable, "-test.run=^TestExecerHelperProcess$", "--", "failure")
	require.ErrorContains(t, err, "subcommand failed: "+executable)
	assert.Nil(t, out)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 23, exitErr.ExitCode())
	out, err = e.Output(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Nil(t, out)
}
