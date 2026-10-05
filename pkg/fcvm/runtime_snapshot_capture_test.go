// adr: 593
package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type capturedRuntimeVMM struct {
	*consumedRuntimeVMM
	seen   SnapshotSpec
	edit   func(*SnapshotInfo)
	before func(context.Context) error
}

func (v *capturedRuntimeVMM) capture(spec SnapshotSpec) (SnapshotInfo, error) {
	v.seen = spec
	parent := spec.admittedParent.Clone()
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: parent, Memory: runtimeadmission.CapturedArtifact{StorageKey: spec.StorageKey, Digest: "sha256:" + strings.Repeat("1", 64), Bytes: 16384}, VMState: runtimeadmission.CapturedArtifact{StorageKey: spec.VMStateStorageKey, Digest: "sha256:" + strings.Repeat("2", 64), Bytes: 4096}, CapturedAtUnixNano: time.Now().UnixNano()}
	for _, drive := range parent.ArtifactConsumption.Drives {
		if drive.Source.Role() == "main" {
			c.PrivateDrive = runtimeadmission.CapturedArtifact{StorageKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: spec.StorageKey}), Digest: "sha256:" + strings.Repeat("3", 64), Bytes: drive.InjectedBytes}
		}
	}
	info := SnapshotInfo{MemBytes: c.Memory.Bytes, VMStateBytes: c.VMState.Bytes, StoredBytes: 4096, Capture: c}
	if v.edit != nil {
		v.edit(&info)
	}
	return info, nil
}

func (v *capturedRuntimeVMM) Snapshot(ctx context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.captureWithContext(ctx, spec)
}

func (v *capturedRuntimeVMM) SnapshotKeepAlive(ctx context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.captureWithContext(ctx, spec)
}

func (v *capturedRuntimeVMM) captureWithContext(ctx context.Context, spec SnapshotSpec) (SnapshotInfo, error) {
	if v.before != nil {
		if err := v.before(ctx); err != nil {
			return SnapshotInfo{}, err
		}
	}
	return v.capture(spec)
}

// Unit and metal fixtures simulate the scheduler's fresh catalog authority.
// Physical capture evidence still comes from the selected native backend.
func managedSnapshotGrant(m *Manager, parent runtimeadmission.Receipt, warm bool) runtimeadmission.SnapshotGrant {
	tier, mode := state.SnapshotTierInit, "park"
	if warm {
		tier, mode = state.SnapshotTierWarm, "warm"
	}
	token := uuid.NewString()
	key := state.SnapshotCaptureMemKey(parent.Binding.DeploymentID, tier, token)
	now := time.Now()
	return runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: token, Parent: parent.Clone(), MemoryKey: key, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: key}), FCVersion: m.fcVersion, Mode: mode, SourceStartedAtUnixNano: parent.CompletedAtUnixNano, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
}

func TestManagedSnapshotUsesOwnedParentAndChecksNativeCapture(t *testing.T) {
	for _, warm := range []bool{false, true} {
		m, original, request := consumedRuntimeFixture(t)
		v := &capturedRuntimeVMM{consumedRuntimeVMM: original}
		m.vmm = v
		inst, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
		if err != nil {
			t.Fatal(err)
		}
		grant := managedSnapshotGrant(m, receipt, warm)
		info, ack, err := m.CaptureAdmitted(t.Context(), grant)
		if err != nil || !info.Capture.Parent.Equal(receipt) || !v.seen.admittedParent.Equal(receipt) || info.Capture.Check(time.Now()) != nil || ack.Check(grant, time.Now()) != nil {
			t.Fatal("caller parent replaced the retained native receipt", err)
		}
		if v.seen.ResumeBeforePublish != warm {
			t.Fatal("warm capture lost its early resume intent")
		}
		info.Capture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-mutated"
		if !inst.runtimeAdmissionReceipt.Equal(receipt) {
			t.Fatal("returned capture mutated the live parent's receipt")
		}
	}
}

func TestManagedSnapshotRejectsMissingOrWrongCaptureAndParksWithoutLeak(t *testing.T) {
	for _, edit := range []func(*SnapshotInfo){
		func(i *SnapshotInfo) { i.Capture = runtimeadmission.SnapshotCapture{} },
		func(i *SnapshotInfo) { i.Capture.Parent.Binding.InstanceID = uuid.NewString() },
		func(i *SnapshotInfo) { i.Capture.Memory.StorageKey = "borrowed" },
		func(i *SnapshotInfo) { i.MemBytes++ },
		func(i *SnapshotInfo) { i.Capture.PrivateDrive.Bytes++ },
	} {
		m, original, request := consumedRuntimeFixture(t)
		v := &capturedRuntimeVMM{consumedRuntimeVMM: original, edit: edit}
		m.vmm = v
		_, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
		if err != nil {
			t.Fatal(err)
		}
		info, ack, err := m.CaptureAdmitted(t.Context(), managedSnapshotGrant(m, receipt, false))
		if err == nil || !info.Capture.IsZero() || ack.Grant.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 {
			t.Fatal("invalid capture retained native resources or evidence", err)
		}
	}
}

func TestManagedSnapshotRefusesStaleInstanceFactsBeforeBackend(t *testing.T) {
	m, original, request := consumedRuntimeFixture(t)
	v := &capturedRuntimeVMM{consumedRuntimeVMM: original}
	m.vmm = v
	inst, _, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	inst.AccountID = uuid.NewString()
	if _, err := m.WarmSnapshot(t.Context(), inst.Lease.Instance, SnapshotSpec{}); err == nil || v.seen.StorageKey != "" || v.seen.admittedParent.Binding.Token != "" {
		t.Fatal("changed runtime ownership reached capture backend", err)
	}
}

func TestManagedSnapshotCanceledAfterNativeCompletionReturnsNoEvidence(t *testing.T) {
	m, original, request := consumedRuntimeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	v := &capturedRuntimeVMM{consumedRuntimeVMM: original, edit: func(*SnapshotInfo) { cancel() }}
	m.vmm = v
	_, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, ack, err := m.CaptureAdmitted(ctx, managedSnapshotGrant(m, receipt, false))
	if !errors.Is(err, context.Canceled) || !info.Capture.IsZero() || ack.Grant.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("canceled capture returned evidence or leaked its lease", err)
	}
}

func TestMeasuredWarmSnapshotSuspendsProbesUntilResume(t *testing.T) {
	m, original, request := consumedRuntimeFixture(t)
	starts, stops := 0, 0
	m.WithLivenessProbes(NewLivenessRegistry(), LivenessProbeConfig{PeriodSeconds: 5, ConsecutiveFailures: 3}).WithLivenessProbeStarter(func(context.Context, string, int, string, LivenessProbeConfig) context.CancelFunc {
		starts++
		return func() { stops++ }
	})
	v := &capturedRuntimeVMM{consumedRuntimeVMM: original}
	m.vmm = v
	_, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatal("admitted wake did not start its liveness probe")
	}
	v.edit = func(*SnapshotInfo) {
		if stops != 1 || starts != 1 {
			t.Fatal("liveness remained active during measured pause")
		}
	}
	if _, _, err := m.CaptureAdmitted(t.Context(), managedSnapshotGrant(m, receipt, true)); err != nil {
		t.Fatal(err)
	}
	if starts != 2 || stops != 1 {
		t.Fatal("measured warm capture did not restart probes after resume")
	}
}
