package bistro

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogger(t *testing.T) {
	for _, tt := range []struct {
		name   string
		log    func(Logger)
		stdout string
		stderr string
	}{
		{
			name:   "info",
			log:    func(l Logger) { l.Info("ready") },
			stdout: "[INFO] ready\n",
		},
		{
			name:   "formatted info",
			log:    func(l Logger) { l.Info("processed %d files in %s", 3, "rootfs") },
			stdout: "[INFO] processed 3 files in rootfs\n",
		},
		{
			name:   "error",
			log:    func(l Logger) { l.Error("failed") },
			stderr: "[ERROR] failed\n",
		},
		{
			name:   "formatted error",
			log:    func(l Logger) { l.Error("missing %s (attempt %d)", "library.so", 2) },
			stderr: "[ERROR] missing library.so (attempt 2)\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			tt.log(NewLogger(&stdout, &stderr))
			assert.Equal(t, tt.stdout, stdout.String())
			assert.Equal(t, tt.stderr, stderr.String())
		})
	}
}
