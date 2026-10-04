package e2etest

import (
	"os"
	"testing"
)

// newHarnessSocketDir keeps Unix socket names short even when TMPDIR points
// to a large acceptance volume through a long mount path. The drive and
// artifact workspaces still use the caller's TMPDIR.
func newHarnessSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "faas-e2e-sock-*")
	if err != nil {
		t.Fatalf("e2etest: mkdir sock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
