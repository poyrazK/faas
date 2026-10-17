package fcvm

// adr: 1525

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type operatorParkCaptureVMM struct{ *fakeVMM }

func (v *operatorParkCaptureVMM) Snapshot(ctx context.Context, l Lease, s SnapshotSpec) (SnapshotInfo, error) {
	_, err := v.fakeVMM.Snapshot(ctx, l, s)
	return SnapshotInfo{MemBytes: 4096, VMStateBytes: 128, StoredBytes: 4224}, err
}

func TestOperatorParkManagerGenerationAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		snapshotErr, killErr error
		want                 string
	}{
		{name: "capture", want: "parked"}, {name: "capture_failure", snapshotErr: errors.New("snapshot failed"), want: "stopped"},
		{name: "cleanup_failure", killErr: errors.New("kill not confirmed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &operatorParkCaptureVMM{fakeVMM: &fakeVMM{snapErr: tc.snapshotErr, killErr: tc.killErr}}
			m := newTestManager(&fakeRunner{}, v)
			const id = "operator-park"
			if _, err := m.ColdBoot(t.Context(), req(id)); err != nil {
				t.Fatal(err)
			}
			g, err := m.OperatorRestartGeneration(id)
			if err != nil {
				t.Fatal(err)
			}
			target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: id, Generation: g}, Action: state.OperatorParkCapture, FCVersion: m.fcVersion}
			stale := target
			stale.Generation = "old-generation"
			if _, err := m.ExecuteOperatorPark(t.Context(), stale); err == nil || len(v.snapshotted) != 0 || len(v.killed) != 0 || m.LiveCount() != 1 {
				t.Fatal("stale generation reached VM", err)
			}
			r, err := m.ExecuteOperatorPark(t.Context(), target)
			if tc.want == "" {
				if err == nil || r.Outcome != "" || m.LeasedCount() != 1 {
					t.Fatalf("unproven cleanup=%+v %v leases=%d", r, err, m.LeasedCount())
				}
				v.killErr = nil
				if err := m.Destroy(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			} else if err != nil || r.Outcome != tc.want || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatalf("result=%+v %v live=%d leases=%d", r, err, m.LiveCount(), m.LeasedCount())
			}
			if _, err := m.ColdBoot(t.Context(), req(id)); err != nil {
				t.Fatal(err)
			}
			if _, err := m.ExecuteOperatorPark(t.Context(), target); err == nil || m.LiveCount() != 1 {
				t.Fatal("old attempt reached replacement", err)
			}
			if err := m.Destroy(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperatorParkMeasuredCaptureRetainsAdmission(t *testing.T) {
	for _, mode := range []string{"valid", "invalid_capture", "cleanup_failure", "wrong_generation", "expired_grant"} {
		t.Run(mode, func(t *testing.T) {
			m, original, request := consumedRuntimeFixture(t)
			v := &capturedRuntimeVMM{consumedRuntimeVMM: original}
			m.vmm = v
			_, parent, err := m.WakeAdmitted(t.Context(), request, nil)
			if err != nil {
				t.Fatal(err)
			}
			g := managedSnapshotGrant(m, parent, false)
			generation, err := m.OperatorRestartGeneration(parent.Binding.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: parent.Binding.InstanceID, Generation: generation}, Action: state.OperatorParkCapture, Measured: true, Grant: &g, MemoryKey: g.MemoryKey, VMStateKey: g.VMStateKey, FCVersion: m.fcVersion}
			switch mode {
			case "invalid_capture":
				v.edit = func(info *SnapshotInfo) { info.MemBytes++ }
			case "cleanup_failure":
				original.fakeVMM.killErr = errors.New("kill unconfirmed")
			case "wrong_generation":
				target.Generation = "replacement"
			case "expired_grant":
				g.IssuedAtUnixNano = time.Now().Add(-2 * time.Minute).UnixNano()
				g.ExpiresAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			}
			r, err := m.ExecuteOperatorPark(t.Context(), target)
			switch mode {
			case "valid":
				if err != nil || r.Outcome != "parked" || r.Acknowledgment == nil || r.Acknowledgment.Check(g, time.Now()) != nil {
					t.Fatalf("measured=%+v %v", r, err)
				}
			case "invalid_capture", "expired_grant":
				if err != nil || r.Outcome != "stopped" || r.Acknowledgment != nil {
					t.Fatalf("fallback=%+v %v", r, err)
				}
			default:
				if err == nil || r.Outcome != "" {
					t.Fatalf("unproven=%+v %v", r, err)
				}
			}
			if mode == "wrong_generation" && (v.seen.StorageKey != "" || m.LiveCount() != 1) {
				t.Fatal("generation mismatch reached capture")
			}
			original.fakeVMM.killErr = nil
			if m.LiveCount() != 0 || m.LeasedCount() != 0 {
				if err := m.Destroy(t.Context(), parent.Binding.InstanceID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
