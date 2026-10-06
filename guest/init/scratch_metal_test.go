//go:build linux && metal

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// Run mounts in a subprocess's private namespace so failures cannot leave
// mounts on the acceptance host. The parent owns and removes the empty targets.
func TestCompanionScratchCapacityIsolationAndCleanup(t *testing.T) {
	if os.Getenv("GREGALE_SCRATCH_CONTRACT_CHILD") == "1" {
		testCompanionScratchMounts(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("scratch mount acceptance requires Linux root")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestCompanionScratchCapacityIsolationAndCleanup$", "-test.v")
	cmd.Env = append(os.Environ(), "GREGALE_SCRATCH_CONTRACT_CHILD=1", "GREGALE_SCRATCH_CONTRACT_ROOT="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scratch mount contract: %v\n%s", err, output)
	}
	for _, name := range []string{"first", "second"} {
		entries, err := os.ReadDir(filepath.Join(root, name, "tmp"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("scratch survived child namespace teardown: %s: entries=%v err=%v", name, entries, err)
		}
	}
}

func testCompanionScratchMounts(t *testing.T) {
	t.Helper()
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("GREGALE_SCRATCH_CONTRACT_ROOT")
	if root == "" {
		t.Fatal("scratch test root missing")
	}
	quotaMB := api.SidecarScratchMBMin
	for _, name := range []string{"first", "second"} {
		workloadRoot := filepath.Join(root, name)
		if err := ensureMountDirectory(workloadRoot); err != nil {
			t.Fatal(err)
		}
		if err := mountSidecarScratch(workloadRoot, quotaMB); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(workloadRoot, "tmp")
		t.Cleanup(func() {
			if err := unix.Unmount(path, 0); err != nil {
				t.Errorf("unmount scratch: %v", err)
			}
		})
		var stat unix.Statfs_t
		if err := unix.Statfs(path, &stat); err != nil {
			t.Fatal(err)
		}
		if stat.Type != unix.TMPFS_MAGIC || stat.Blocks*uint64(stat.Bsize) != uint64(quotaMB)*1024*1024 {
			t.Fatalf("unexpected scratch filesystem or capacity: %+v", stat)
		}
		if stat.Flags&(unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_NOSUID|unix.ST_NODEV {
			t.Fatalf("scratch permits setuid or device interpretation: flags=%x", stat.Flags)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o777 || info.Mode()&os.ModeSticky == 0 {
			t.Fatalf("scratch does not have sticky world-writable permissions: %s", info.Mode())
		}
	}
	first := filepath.Join(root, "first", "tmp", "payload")
	file, err := os.Create(first)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 64*1024)
	var written int
	for written <= quotaMB*1024*1024 {
		n, writeErr := file.Write(chunk)
		written += n
		if writeErr != nil {
			err = writeErr
			break
		}
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !errors.Is(err, unix.ENOSPC) || written != quotaMB*1024*1024 {
		t.Fatalf("scratch ceiling: written=%d err=%v", written, err)
	}
	second := filepath.Join(root, "second", "tmp", "payload")
	if _, err := os.Stat(second); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch content crossed mount boundary: %v", err)
	}
	if err := os.WriteFile(second, []byte("independent"), 0o600); err != nil {
		t.Fatalf("exhausted scratch affected another mount: %v", err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("reclaimed"), 0o600); err != nil {
		t.Fatalf("scratch capacity not reclaimed after deletion: %v", err)
	}
}
