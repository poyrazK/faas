package fcvm

import (
	"context"
	"errors"
	"testing"
)

type failedParkVMM struct {
	*fakeVMM
	cancel  context.CancelFunc
	killCtx error
}

func (v *failedParkVMM) Snapshot(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
	v.cancel()
	return SnapshotInfo{}, context.DeadlineExceeded
}

func (v *failedParkVMM) Kill(ctx context.Context, l Lease) error {
	v.killCtx = ctx.Err()
	return v.fakeVMM.Kill(ctx, l)
}

func TestParkDeadlineKillsPausedVMWithoutWaitingForExport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vmm := &failedParkVMM{fakeVMM: &fakeVMM{}, cancel: cancel}
	runner := &fakeRunner{}
	m := newTestManager(runner, vmm)
	if _, err := m.ColdBoot(ctx, req("park-deadline")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Park(ctx, "park-deadline", SnapshotSpec{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Park error = %v, want capture deadline", err)
	}
	if len(vmm.destroyedWithExport) != 0 {
		t.Error("failed app capture entered the wait-for-builder-exit path")
	}
	if len(vmm.killed) == 0 || vmm.killCtx != nil {
		t.Fatalf("kill calls=%v, cleanup context error=%v", vmm.killed, vmm.killCtx)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || len(m.cidToID) != 0 {
		t.Fatalf("leaked live=%d leases=%d CIDs=%d", m.LiveCount(), m.LeasedCount(), len(m.cidToID))
	}
	if !runner.ran("ip netns del") {
		t.Error("failed capture did not tear down the network namespace")
	}
}
