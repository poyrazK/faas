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
	seen SnapshotSpec
	edit func(*SnapshotInfo)
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

func (v *capturedRuntimeVMM) Snapshot(_ context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.capture(spec)
}

func (v *capturedRuntimeVMM) SnapshotKeepAlive(_ context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.capture(spec)
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
		key := state.SnapshotCaptureMemKey(receipt.Binding.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
		spec := SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), admittedParent: receipt.Clone()}
		spec.admittedParent.Binding.InstanceID = uuid.NewString()
		var info SnapshotInfo
		if warm {
			info, err = m.WarmSnapshot(t.Context(), inst.Lease.Instance, spec)
		} else {
			info, err = m.Park(t.Context(), inst.Lease.Instance, spec)
		}
		if err != nil || !info.Capture.Parent.Equal(receipt) || !v.seen.admittedParent.Equal(receipt) || info.Capture.Check(time.Now()) != nil {
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
		inst, _, err := m.WakeAdmitted(t.Context(), request, nil)
		if err != nil {
			t.Fatal(err)
		}
		key := state.SnapshotCaptureMemKey(inst.DeploymentID, state.SnapshotTierInit, uuid.NewString())
		info, err := m.Park(t.Context(), inst.Lease.Instance, SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})})
		if err == nil || !info.Capture.IsZero() || m.LiveCount() != 0 || m.LeasedCount() != 0 {
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
	inst, _, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := state.SnapshotCaptureMemKey(inst.DeploymentID, state.SnapshotTierInit, uuid.NewString())
	info, err := m.Park(ctx, inst.Lease.Instance, SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})})
	if !errors.Is(err, context.Canceled) || !info.Capture.IsZero() || m.LiveCount() != 0 || m.LeasedCount() != 0 {
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
	inst, _, err := m.WakeAdmitted(t.Context(), request, nil)
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
	key := state.SnapshotCaptureMemKey(inst.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
	if _, err := m.WarmSnapshot(t.Context(), inst.Lease.Instance, SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})}); err != nil {
		t.Fatal(err)
	}
	if starts != 2 || stops != 1 {
		t.Fatal("measured warm capture did not restart probes after resume")
	}
}
