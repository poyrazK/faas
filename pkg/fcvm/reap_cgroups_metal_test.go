//go:build metal && linux

// adr: 631
package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestMetalReapOrphanedTenantCgroups runs the sweep against real cgroup v2:
// an empty unowned scope (with an empty child, like a workload scope) is
// removed by rmdir, and a scope holding a process is left alone.
func TestMetalReapOrphanedTenantCgroups(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root for cgroupfs")
	}
	// TestMain points cgroupRoot at a temp dir; this test needs the kernel's.
	const root = "/sys/fs/cgroup"
	parent := "faas-adr631-metal.slice"
	parentPath := filepath.Join(root, parent)
	if err := os.Mkdir(parentPath, 0o755); err != nil {
		t.Skipf("cannot create test cgroup: %v", err)
	}
	const idEmpty, idBusy = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa", "cccccccc-3333-4333-8333-cccccccccccc"
	for _, dir := range []string{idEmpty, filepath.Join(idEmpty, "workload-main"), idBusy} {
		if err := os.Mkdir(filepath.Join(parentPath, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sleeper.Process.Kill()
		_ = sleeper.Wait()
		_ = os.Remove(filepath.Join(parentPath, idBusy))
		_ = os.Remove(filepath.Join(parentPath, idEmpty, "workload-main"))
		_ = os.Remove(filepath.Join(parentPath, idEmpty))
		_ = os.Remove(parentPath)
	})
	if err := os.WriteFile(filepath.Join(parentPath, idBusy, "cgroup.procs"), []byte(strconv.Itoa(sleeper.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := ReapOrphanedTenantCgroups(context.Background(), TenantCgroupReapOptions{
		Root:    root,
		Parents: []string{parent},
		IsLive:  func(context.Context, string) (bool, error) { return false, nil },
		MinAge:  time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Reaped != 1 || rep.SkippedBusy != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want one reaped and one busy", rep)
	}
	if _, err := os.Stat(filepath.Join(parentPath, idEmpty)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty scope survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parentPath, idBusy)); err != nil {
		t.Fatalf("populated scope was removed: %v", err)
	}
}
