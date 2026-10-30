package bistro

import (
	"fmt"
	"io"
)

type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

type defaultLogger struct {
	stdOut io.Writer
	stdErr io.Writer
}

func NewLogger(stdout, stderr io.Writer) Logger {
	return &defaultLogger{
		stdOut: stdout,
		stdErr: stderr,
	}
}

func (l *defaultLogger) Info(msg string, args ...any) {
	fmt.Fprintf(l.stdOut, "[INFO] "+msg+"\n", args...)
}

func (l *defaultLogger) Error(msg string, args ...any) {
	fmt.Fprintf(l.stdErr, "[ERROR] "+msg+"\n", args...)
}
