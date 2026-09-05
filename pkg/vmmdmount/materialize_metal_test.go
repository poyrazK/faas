//go:build metal && linux

package vmmdmount

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMetalCopyParentTreeUnprivilegedHandoff(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for cross-UID handoff")
	}
	source, target := t.TempDir(), t.TempDir()
	if err := os.Chmod(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "parent"), []byte("root-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	const workerUID = 65534
	if err := os.Chown(target, workerUID, workerUID); err != nil {
		t.Fatal(err)
	}
	if err := copyParentTree(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Sys().(*syscall.Stat_t).Uid; got != workerUID {
		t.Fatalf("staging owner = %d, want %d", got, workerUID)
	}
	info, err = os.Stat(filepath.Join(target, "parent"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Sys().(*syscall.Stat_t).Uid; got != 0 {
		t.Fatalf("parent child owner = %d, want 0", got)
	}
	// Inherit the staging FD to avoid the testing framework's private root
	// ancestors. The child still needs write permission on the staging root.
	dir, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	cmd := exec.Command("mkdir", "/proc/self/fd/3/app")
	cmd.ExtraFiles = []*os.File{dir}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: workerUID, Gid: workerUID}}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged runtime delta mkdir: %v: %s", err, out)
	}
}
