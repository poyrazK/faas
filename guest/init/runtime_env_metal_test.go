//go:build linux && metal

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The overlay runs in a subprocess's private mount namespace so a failure
// cannot leave mounts on the acceptance host (ADR-20261025111339).
func TestGuestTmpKeepsImageFiles(t *testing.T) {
	if os.Getenv("GREGALE_GUEST_TMP_CHILD") == "1" {
		testGuestTmpOverlay(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("guest /tmp overlay requires Linux root")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	image := filepath.Join(root, "tmp")
	if err := os.Mkdir(image, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(image, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "baked"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestGuestTmpKeepsImageFiles$", "-test.v")
	cmd.Env = append(os.Environ(), "GREGALE_GUEST_TMP_CHILD=1", "GREGALE_GUEST_TMP_ROOT="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("guest /tmp overlay: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(image, "written")); !os.IsNotExist(err) {
		t.Fatalf("a write reached the image's /tmp: %v", err)
	}
}

func testGuestTmpOverlay(t *testing.T) {
	t.Helper()
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("GREGALE_GUEST_TMP_ROOT")
	image, staging := filepath.Join(root, "tmp"), filepath.Join(root, "dev")
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mountTmpOverImage(image, staging); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Unmount(image, unix.MNT_DETACH) }()
	if got, err := os.ReadFile(filepath.Join(image, "baked")); err != nil || string(got) != "image" {
		t.Fatalf("image file under the overlay: %q %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(image, "written"), []byte("guest"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(image)
	if err != nil || info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0o777 {
		t.Fatalf("overlay root mode = %v (%v), want the image's sticky 0777", info.Mode(), err)
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(image, &stat); err != nil || stat.Type != unix.OVERLAYFS_SUPER_MAGIC {
		t.Fatalf("/tmp filesystem = %#x (%v), want overlay", stat.Type, err)
	}
	if entries, err := os.ReadDir(staging); err != nil || len(entries) != 0 {
		t.Fatalf("staging left behind: %v %v", entries, err)
	}
}
