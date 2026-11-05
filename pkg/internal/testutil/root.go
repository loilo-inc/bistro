// Package testutil provides shared test helpers.
package testutil

import (
	"os"
	"testing"
)

func RequireRoot(t testing.TB) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root to restore archive ownership")
	}
}
