package bistro

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type Execer interface {
	Output(name string, arg ...string) ([]byte, error)
}

type defaultExecer struct {
}

func NewExecer() Execer {
	return &defaultExecer{}
}

var _ Execer = &defaultExecer{}

func (e *defaultExecer) Output(name string, arg ...string) ([]byte, error) {
	cmd := exec.Command(name, arg...)
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	// Set environment variables to ensure consistent locale of dpkg-query
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C", "LANG=C")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitErr.Stderr = stderr.Bytes()
		}
		return nil, errors.Join(
			fmt.Errorf("subcommand failed: %s", name),
			err,
		)
	}
	return output, nil
}
