package bistro

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

type Execer interface {
	Output(name string, arg ...string) ([]byte, error)
}

type DefaultExecer struct {
}

var _ Execer = &DefaultExecer{}

func (e *DefaultExecer) Output(name string, arg ...string) ([]byte, error) {
	cmd := exec.Command(name, arg...)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("subcommand failed: %s", name),
			err,
		)
	}
	return output, nil
}
