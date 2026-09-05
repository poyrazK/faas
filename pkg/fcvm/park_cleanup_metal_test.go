//go:build linux && metal

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

type pausedCaptureFailureVMM struct {
	*JailerVMM
	cancel context.CancelFunc
}

func (v *pausedCaptureFailureVMM) Snapshot(ctx context.Context, l Lease, _ SnapshotSpec) (SnapshotInfo, error) {
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return SnapshotInfo{}, err
	}
	v.cancel()
	return SnapshotInfo{}, context.DeadlineExceeded
}

// Run using run_optimization_metal.py's private mount/network namespaces.
func TestMetalParkDeadlineReleasesPausedVM(t *testing.T) {
	helper := os.Getenv("FAAS_TEST_VMMD_HELPER")
	if helper == "" {
		t.Skip("set FAAS_TEST_VMMD_HELPER with isolated network and jail mounts")
	}
	kernel, base, layer := metalImages(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	runner := wire.ExecRunner{}
	for _, argv := range [][]string{
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := runner.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = runner.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vmm := &pausedCaptureFailureVMM{JailerVMM: NewJailerVMM(JailChrootBase, 30*time.Second)}
	vmm.mountHelperPath = helper
	m := NewManager(runner, vmm, Paths{Kernel: kernel}, version, nil, nil)
	m.alloc.free = []int{MaxSlots - 1}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for cycle := 0; cycle < 10; cycle++ {
		name := fmt.Sprintf("optpark-fail-%d", cycle)
		t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), name, 0, 0) })
		inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: name, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby"})
		if err != nil {
			t.Fatal(err)
		}
		parkCtx, cancelPark := context.WithCancel(ctx)
		vmm.cancel = cancelPark
		start := time.Now()
		_, err = m.Park(parkCtx, name, SnapshotSpec{})
		cancelPark()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("park error = %v, want injected capture deadline", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("cleanup took %v; paused app waited for an export exit", elapsed)
		}
		if m.LiveCount() != 0 || m.LeasedCount() != 0 {
			t.Fatalf("leaked live=%d leases=%d", m.LiveCount(), m.LeasedCount())
		}
		for _, path := range []string{
			filepath.Join("/run/netns", inst.Lease.Netns),
			vmm.chrootRoot(name),
			filepath.Join("/sys/fs/cgroup", ParentCgroupFor(inst.Lease.Plan), PerInstanceScope(name)),
		} {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("resource remains after failed park: %s (%v)", path, err)
			}
		}
		t.Logf("cycle=%d failed_park_cleanup_ms=%.3f", cycle, float64(time.Since(start))/float64(time.Millisecond))
	}
}
