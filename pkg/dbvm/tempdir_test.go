package dbvm

import (
	"os"
	"testing"
)

// shortTempDir returns a short temp dir: AF_UNIX paths are limited to
// ~104 bytes and t.TempDir() can exceed that on macOS.
func shortTempDir(t *testing.T) (string, error) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dbvm")
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir, nil
}
