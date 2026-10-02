//go:build metal && linux && amd64

package fcvm

// adr: 431. These checks require the dedicated native KVM fixture and leakcheck.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func nativeMetalSnapshotSpec(inst *Instance, tier string) SnapshotSpec {
	key := state.SnapshotCaptureMemKey(inst.DeploymentID, tier, uuid.NewString())
	return SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})}
}

func assertMetalSnapshotCapture(t *testing.T, ctx context.Context, backend storage.StorageBackend, info SnapshotInfo, parent runtimeadmission.Receipt) {
	t.Helper()
	capture := info.Capture
	if capture.Check(time.Now()) != nil || !capture.Parent.Equal(parent) || capture.Memory.Bytes != info.MemBytes || capture.VMState.Bytes != info.VMStateBytes || info.StoredBytes <= 0 {
		t.Fatal("snapshot did not retain the exact measured parent and byte facts")
	}
	for _, artifact := range []runtimeadmission.CapturedArtifact{capture.Memory, capture.VMState, capture.PrivateDrive} {
		reader, err := backend.Get(ctx, artifact.StorageKey)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := rootfs.ReadArtifactIdentity(ctx, reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil || identity.Digest != artifact.Digest || identity.Bytes != artifact.Bytes {
			t.Fatal("published snapshot differed from native paused capture", err, closeErr)
		}
	}
}

func TestMetalNativeSnapshotCaptureMeasuresWarmAndParkAndPreservesRetry(t *testing.T) {
	f := newMetalRuntimeDriveFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	inst := f.boot(t, ctx)
	parent := inst.runtimeAdmissionReceipt.Clone()
	spec := nativeMetalSnapshotSpec(inst, state.SnapshotTierWarm)
	warm, err := f.manager.WarmSnapshot(ctx, inst.Lease.Instance, spec)
	if err != nil {
		t.Fatal(err)
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, warm, parent)
	if _, err := f.manager.WarmSnapshot(ctx, inst.Lease.Instance, spec); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("retry overwrote an already published native capture", err)
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, warm, parent)
	parked, err := f.manager.Park(ctx, inst.Lease.Instance, nativeMetalSnapshotSpec(inst, state.SnapshotTierInit))
	if err != nil {
		t.Fatal(err)
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, parked, parent)
	if f.manager.LiveCount() != 0 || f.manager.LeasedCount() != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 || f.vmm.runtimeSources().root != "" {
		t.Fatal("native park retained process, drive or source ownership")
	}
}

func TestMetalNativeSnapshotFailedPublicationRemovesFreshTriple(t *testing.T) {
	f := newMetalRuntimeDriveFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	inst := f.boot(t, ctx)
	spec := nativeMetalSnapshotSpec(inst, state.SnapshotTierWarm)
	warm, err := f.manager.WarmSnapshot(ctx, inst.Lease.Instance, spec)
	if err != nil {
		t.Fatal(err)
	}
	failed := nativeMetalSnapshotSpec(inst, state.SnapshotTierInit)
	driveKey := state.SnapshotDriveKey(state.Snapshot{StorageKey: failed.StorageKey})
	f.vmm.storage = &failedMemoryPublication{StorageBackend: f.backend, failKey: driveKey}
	info, err := f.manager.Park(ctx, inst.Lease.Instance, failed)
	if !errors.Is(err, context.DeadlineExceeded) || !info.Capture.IsZero() {
		t.Fatal("failed private-drive upload returned capture evidence", err)
	}
	for _, key := range []string{failed.StorageKey, failed.VMStateStorageKey, driveKey} {
		reader, err := f.backend.Get(ctx, key)
		if reader != nil {
			_ = reader.Close()
		}
		if !storage.IsNotFound(err) {
			t.Fatal("failed capture left a published part", err)
		}
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, warm, warm.Capture.Parent)
	if f.manager.LiveCount() != 0 || f.manager.LeasedCount() != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 {
		t.Fatal("failed native capture retained runtime ownership")
	}
}
