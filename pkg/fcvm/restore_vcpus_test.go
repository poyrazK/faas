// adr: 20261101160231

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVCPUThreadCount(t *testing.T) {
	root := t.TempDir()
	task := filepath.Join(root, "4242", "task")
	for tid, comm := range map[string]string{
		"4242": "firecracker", "4243": "fc_api", "4244": "fc_vcpu 0", "4245": "fc_vcpu 1", "4246": "fc_vmm",
	} {
		if err := os.MkdirAll(filepath.Join(task, tid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(task, tid, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A thread that exited between the listing and the read has no comm.
	if err := os.MkdirAll(filepath.Join(task, "4247"), 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := vcpuThreadCount(root, 4242); err != nil || n != 2 {
		t.Fatalf("vcpuThreadCount = %d, %v; want 2 fc_vcpu threads", n, err)
	}
	if _, err := vcpuThreadCount(root, 1); err == nil {
		t.Fatal("vcpuThreadCount of a missing process: want an error")
	}
}

func TestRestoredVCPUsMatch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want int
		mismatch  bool
	}{
		{"same count restores", 2, 2, false},
		{"one-vCPU wake refuses a two-vCPU snapshot", 2, 1, true},
		{"two-vCPU wake refuses a one-vCPU snapshot", 1, 2, true},
		{"no vCPU thread seen keeps the restore", 0, 1, false},
		{"a wake without a count skips the check", 2, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := restoredVCPUsMatch(tc.got, tc.want)
			if got := errors.Is(err, ErrSnapshotVCPUMismatch); got != tc.mismatch {
				t.Fatalf("restoredVCPUsMatch(%d, %d) = %v; mismatch = %v, want %v", tc.got, tc.want, err, got, tc.mismatch)
			}
		})
	}
}

// A vCPU mismatch is the snapshot's property, not this node's: the fallback
// must let schedd retire the snapshot so the next park captures at the new
// count.
func TestRestoreVCPUMismatchIsNotNodeLocal(t *testing.T) {
	err := fmt.Errorf("vmm: %w", restoredVCPUsMatch(2, 1))
	if RestoreFailureNodeLocal(err) {
		t.Fatalf("RestoreFailureNodeLocal(%v) = true; the snapshot would stay live and every wake would refuse it", err)
	}
}
