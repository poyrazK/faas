//go:build linux || darwin

package runrelease

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReleaseFIFOInputCannotBlockRootPreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("FIFO input accepted")
	}
}
