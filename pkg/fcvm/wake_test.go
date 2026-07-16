package fcvm

import (
	"context"
	"fmt"
	"testing"
)

func wakeReq(id string, snap *Snapshot) WakeRequest {
	return WakeRequest{Instance: id, BasePath: "/b.ext4", LayerPath: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap}
}

func TestWakeRestoresUsableSnapshot(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", usableSnapshot()))
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Errorf("method = %s, want restore", inst.Method)
	}
	if !vmm.restoredInstance("i1") {
		t.Error("restore was not called")
	}
	if vmm.boots() != 0 {
		t.Error("cold boot should not run when restore succeeds")
	}
}

func TestWakeStaleSnapshotColdBoots(t *testing.T) {
	snap := usableSnapshot()
	snap.Stale = true
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", snap))
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("stale snapshot should cold boot, got %s", inst.Method)
	}
	if vmm.restoredInstance("i1") {
		t.Error("stale snapshot must not be restored")
	}
	if vmm.boots() != 1 {
		t.Errorf("expected exactly 1 cold boot, got %d", vmm.boots())
	}
}

func TestWakeVersionMismatchColdBoots(t *testing.T) {
	snap := usableSnapshot()
	snap.FCVersion = "0.0.1" // manager runs testFCVersion
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", snap))
	if err != nil {
		t.Fatal(err)
	}
	if inst.Method != WakeColdBoot || vmm.restoredInstance("i1") {
		t.Error("version-mismatched snapshot must cold boot, not restore")
	}
}

// TestWakeRestoreFailureFallsBackToColdBoot is the ADR-005 guarantee: a usable
// snapshot that fails to restore must still bring the app up via cold boot, and
// leak nothing.
func TestWakeRestoreFailureFallsBackToColdBoot(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("corrupt mem file")}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", usableSnapshot()))
	if err != nil {
		t.Fatalf("wake should recover via cold boot, got %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("fallback method should read cold_boot (so schedd re-snapshots), got %s", inst.Method)
	}
	if !vmm.restoredInstance("i1") {
		t.Error("restore should have been attempted first")
	}
	if vmm.boots() != 1 {
		t.Errorf("expected 1 cold boot after restore failure, got %d", vmm.boots())
	}
	// The half-restored VM must have been killed before cold boot.
	if len(vmm.killed) == 0 {
		t.Error("half-restored VM should be killed before fallback")
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Errorf("live=%d leased=%d, want 1/1", m.LiveCount(), m.LeasedCount())
	}
}

// TestWakeTotalFailureNoLeak: restore fails AND cold boot fails => terminal error
// and zero leaked resources.
func TestWakeTotalFailureNoLeak(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("bad snapshot"), bootErr: fmt.Errorf("no kvm")}
	m := newTestManager(run, vmm)

	if _, err := m.Wake(context.Background(), wakeReq("i1", usableSnapshot())); err == nil {
		t.Fatal("expected terminal error when both restore and cold boot fail")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leased=%d, want 0 — LEASE LEAK", m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Error("network should be torn down on total wake failure")
	}
}

func TestParkSnapshotsAndReleases(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	if _, err := m.Wake(context.Background(), wakeReq("i1", nil)); err != nil {
		t.Fatal(err)
	}

	info, err := m.Park(context.Background(), "i1", SnapshotSpec{MemPath: "/snap/mem", VMStatePath: "/snap/state"})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if info.MemBytes == 0 {
		t.Error("park should report snapshot size")
	}
	if len(vmm.snapshotted) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(vmm.snapshotted))
	}
	// Invariant §6.2-4: parked app holds zero resident resources.
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("after park live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Error("park should tear down the network")
	}
}

func TestParkSnapshotFailureDestroysAndReleases(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{snapErr: fmt.Errorf("disk full")}
	m := newTestManager(run, vmm)
	if _, err := m.Wake(context.Background(), wakeReq("i1", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Park(context.Background(), "i1", SnapshotSpec{MemPath: "/m", VMStatePath: "/s"}); err == nil {
		t.Fatal("park should surface the snapshot error")
	}
	// Even on snapshot failure the instance is destroyed (rootfs keeps it
	// cold-bootable) and nothing leaks.
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("after failed park live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
}

func TestParkUnknownInstanceErrors(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if _, err := m.Park(context.Background(), "ghost", SnapshotSpec{MemPath: "/m", VMStatePath: "/s"}); err == nil {
		t.Error("parking an unknown instance should error")
	}
}
