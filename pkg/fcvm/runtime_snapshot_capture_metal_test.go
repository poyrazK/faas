//go:build metal && linux && amd64

// adr: 592
package fcvm

// adr: 435. These checks require the dedicated native KVM fixture and leakcheck.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/storage"
)

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
	grant := managedSnapshotGrant(f.manager, parent, true)
	warm, ack, err := f.manager.CaptureAdmitted(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, warm, parent)
	if ack.Check(grant, time.Now()) != nil {
		t.Fatal("native warm capture did not acknowledge the issued grant")
	}
	if _, _, err := f.manager.CaptureAdmitted(ctx, grant); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("retry overwrote an already published native capture", err)
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, warm, parent)
	parkGrant := managedSnapshotGrant(f.manager, parent, false)
	parked, ack, err := f.manager.CaptureAdmitted(ctx, parkGrant)
	if err != nil {
		t.Fatal(err)
	}
	assertMetalSnapshotCapture(t, ctx, f.backend, parked, parent)
	if ack.Check(parkGrant, time.Now()) != nil {
		t.Fatal("native park did not acknowledge the issued grant")
	}
	if f.manager.LiveCount() != 0 || f.manager.LeasedCount() != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 || f.vmm.runtimeSources().root != "" {
		t.Fatal("native park retained process, drive or source ownership")
	}
}

func TestMetalNativeSnapshotFailedPublicationRemovesFreshTriple(t *testing.T) {
	f := newMetalRuntimeDriveFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	inst := f.boot(t, ctx)
	warm, _, err := f.manager.CaptureAdmitted(ctx, managedSnapshotGrant(f.manager, inst.runtimeAdmissionReceipt, true))
	if err != nil {
		t.Fatal(err)
	}
	failed := managedSnapshotGrant(f.manager, inst.runtimeAdmissionReceipt, false)
	driveKey := failed.PrivateDriveKey
	f.vmm.storage = &failedMemoryPublication{StorageBackend: f.backend, failKey: driveKey}
	info, ack, err := f.manager.CaptureAdmitted(ctx, failed)
	if !errors.Is(err, context.DeadlineExceeded) || !info.Capture.IsZero() || ack.Grant.Token != "" {
		t.Fatal("failed private-drive upload returned capture evidence", err)
	}
	for _, key := range []string{failed.MemoryKey, failed.VMStateKey, driveKey} {
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
