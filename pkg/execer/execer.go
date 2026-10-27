package execer

import "os/exec"

type Execer interface {
	Output(name string, arg ...string) ([]byte, error)
}

type DefaultExecer struct {
}

var _ Execer = &DefaultExecer{}

func (e *DefaultExecer) Output(name string, arg ...string) ([]byte, error) {
	return exec.Command(name, arg...).Output()
}
