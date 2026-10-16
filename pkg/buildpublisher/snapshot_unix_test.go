//go:build unix

package buildpublisher

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestExportRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MeasureExport(t.Context(), path); err == nil {
		t.Fatal("FIFO accepted by publisher")
	}
	if _, err := SnapshotExport(t.Context(), path, "sha256:"+strings.Repeat("0", 64), 8); err == nil {
		t.Fatal("FIFO accepted by consumer")
	}
}
