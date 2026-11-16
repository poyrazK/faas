//go:build linux

package vmmdgrpc

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A second close of a bridge unit's pidfd must not close the file that
// reused its number (production fsn-2: the resource journal's pending
// record, "close …/.pending-…: bad file descriptor").
func TestPidfdProcessCloseTwiceLeavesAReusedNumberAlone(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Skipf("pidfd_open: %v", err)
	}
	p := &pidfdProcess{fd: fd}
	if !p.alive() {
		t.Fatal("own process not alive through its pidfd")
	}
	p.close()
	if p.alive() {
		t.Fatal("alive after close")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "pending"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if int(f.Fd()) != fd {
		t.Logf("the kernel gave the file %d, not the closed pidfd's %d; the check still runs", f.Fd(), fd)
	}
	p.close()
	if _, err := f.Write([]byte("record")); err != nil {
		t.Fatalf("write after the second close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close after the second close: %v", err)
	}
}
