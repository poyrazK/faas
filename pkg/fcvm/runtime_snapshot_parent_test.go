package fcvm

// adr: 595 Portable capture forwarding uses an explicitly simulated restored process.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type capturedRestoredRuntimeVMM struct {
	*consumedSnapshotTestVMM
	captured capturedRuntimeVMM
}

func (v *capturedRestoredRuntimeVMM) Snapshot(ctx context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.captured.captureWithContext(ctx, spec)
}

func (v *capturedRestoredRuntimeVMM) SnapshotKeepAlive(ctx context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.captured.captureWithContext(ctx, spec)
}

func TestManagedSnapshotRestoredParentRetainsActualServingReceipt(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "park", true: "warm"}[warm], func(t *testing.T) {
			m, restored, req := admittedSnapshotFixture(t, false)
			v := &capturedRestoredRuntimeVMM{consumedSnapshotTestVMM: restored}
			m.vmm = v
			inst, parent, err := m.WakeAdmitted(t.Context(), req, nil)
			if err != nil || parent.SnapshotConsumption.IsZero() {
				t.Fatal("simulated serving restore did not retain a parent", err)
			}
			grant := managedSnapshotGrant(m, parent, warm)
			info, ack, err := m.CaptureAdmitted(t.Context(), grant)
			if err != nil || !info.Capture.Parent.Equal(parent) || !ack.Capture.Parent.Equal(parent) || !v.captured.seen.admittedParent.Equal(parent) || ack.Check(grant, time.Now()) != nil {
				t.Fatal("new capture lost the actual serving restore receipt", err)
			}
			if grant.Token == parent.SnapshotConsumption.CaptureToken || info.Capture.Memory.StorageKey == parent.SnapshotConsumption.Memory.StorageKey {
				t.Fatal("restored input namespace was reused for capture output")
			}
			info.Capture.Parent.SnapshotConsumption.Memory.Digest = "reader-change"
			if !inst.runtimeAdmissionReceipt.Equal(parent) {
				t.Fatal("capture reader mutated the serving receipt")
			}
			if !warm && (m.LiveCount() != 0 || m.LeasedCount() != 0) {
				t.Fatal("parked restored source retained native resources")
			}
			if warm && (m.LiveCount() != 1 || m.LeasedCount() != 1 || runtimeadmission.CheckSnapshotParent(inst.runtimeAdmissionReceipt) != nil) {
				t.Fatal("warm capture replaced serving identity")
			}
		})
	}
}
