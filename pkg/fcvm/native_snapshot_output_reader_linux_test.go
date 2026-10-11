//go:build linux

// adr: 794 — descriptor checks establish no native mount or VM acceptance.
package fcvm

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeSnapshotOutputReaderRejectsLinuxPathOnlyDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture")
	if err := os.WriteFile(path, []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := resourceFileID(info)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "path-only-capture")
	defer file.Close()
	if err := checkNativeSnapshotOutputDescriptor(file, nativeLoopIdentity{Device: id.Device, Inode: id.Inode}); err == nil {
		t.Fatal("O_PATH descriptor authorized capture reads")
	}
}
