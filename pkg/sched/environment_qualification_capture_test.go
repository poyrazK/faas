package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Portable ordering fixtures do not establish Firecracker capture acceptance.
type qualificationCaptureVMM struct {
	*qualificationRuntimeVMM
	captures int
	change   func(*EnvironmentQualificationSnapshotEvidence)
}

func (v *qualificationCaptureVMM) CaptureEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	v.captures++
	// Native retirement acknowledges the same immutable generation used as
	// the capture receipt ID. Keep the storage fixture bound to that identity.
	capture := v.proof.ReceiptID
	snapshot := state.Snapshot{StorageKey: state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, capture)}
	evidence := EnvironmentQualificationSnapshotEvidence{Execution: frame, Snapshot: state.EnvironmentQualificationSnapshot{
		CaptureID: capture, NativeGeneration: v.proof.NativeGeneration, KernelBootID: v.proof.KernelBootID, FCVersion: "test-fc",
		StorageKey: snapshot.StorageKey, VMStateStorageKey: state.SnapshotVMStateKey(snapshot), DriveStorageKey: state.SnapshotDriveKey(snapshot),
		BackingStorageKey: state.SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048}}
	if v.change != nil {
		v.change(&evidence)
	}
	return evidence, nil
}

type lostCaptureResponseStore struct {
	*state.MemStore
	lost bool
}

func (s *lostCaptureResponseStore) RecordEnvironmentQualificationSnapshot(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest, frame state.EnvironmentQualificationExecution, proof state.EnvironmentQualificationSnapshot) (state.EnvironmentQualificationSnapshotReceipt, error) {
	receipt, err := s.MemStore.RecordEnvironmentQualificationSnapshot(ctx, claimed, frame, proof)
	if err == nil && !s.lost {
		s.lost = true
		return state.EnvironmentQualificationSnapshotReceipt{}, errors.New("lost commit response")
	}
	return receipt, err
}

func TestEnvironmentQualificationCaptureReplaysCommittedEvidenceWithoutVMEffects(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	wrapped := &lostCaptureResponseStore{MemStore: store}
	v := &qualificationCaptureVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	notify := &fakeNotifier{}
	e := newEngine(t, store, v, notify, "test-fc")
	e.store = wrapped
	err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
		if _, err := e.CaptureEnvironmentWorkloadQualification(ctx, request, ins); err == nil {
			t.Fatal("lost response was not surfaced")
		}
		first, err := store.EnvironmentQualificationSnapshotReceipt(ctx, ins.ID)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := e.CaptureEnvironmentWorkloadQualification(ctx, request, ins)
		if err != nil || retry.Snapshot != first.Snapshot || !retry.RecordedAt.Equal(first.RecordedAt) || v.captures != 1 {
			t.Fatalf("retry recaptured or replaced committed evidence: %+v captures=%d %v", retry, v.captures, err)
		}
		forged := ins
		forged.WakeID = uuid.NewString()
		if _, err := e.CaptureEnvironmentWorkloadQualification(ctx, request, forged); !errors.Is(err, state.ErrConflict) || v.captures != 1 {
			t.Fatalf("foreign incarnation borrowed capture: %v", err)
		}
		return nil
	})
	if err != nil || e.ledger.ResidentRAM() != 0 || notify.count(db.NotifyDeploymentReady) != 0 {
		t.Fatalf("capture changed activation or retirement: ram=%d %v", e.ledger.ResidentRAM(), err)
	}
	if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), request.ReservedInstanceID); err != nil {
		t.Fatal("retirement lost committed capture evidence", err)
	}
}

func TestEnvironmentQualificationCaptureRejectsSubstitutedNativeEvidence(t *testing.T) {
	for _, failure := range []string{"frame", "namespace", "bytes"} {
		t.Run(failure, func(t *testing.T) {
			store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
			v := &qualificationCaptureVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
			v.change = func(e *EnvironmentQualificationSnapshotEvidence) {
				switch failure {
				case "frame":
					e.Execution.Attempt++
				case "namespace":
					e.Snapshot.StorageKey = state.SnapMemKey(e.Execution.DeploymentID)
				case "bytes":
					e.Snapshot.MemBytes = 0
				}
			}
			e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
			err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
				_, err := e.CaptureEnvironmentWorkloadQualification(ctx, request, ins)
				return err
			})
			if !errors.Is(err, state.ErrConflict) || e.ledger.ResidentRAM() != 0 || v.captures != 1 {
				t.Fatalf("invalid capture accepted or leaked admission: %v", err)
			}
			if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), request.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("invalid capture became durable: %v", err)
			}
		})
	}
}
