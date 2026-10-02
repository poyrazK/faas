package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardRuntimeReceiptTestStore interface {
	standardSnapshotTestStore
	InstanceApplicationStandardRuntimeReceiptStore
}

func standardRuntimeReceiptOwnedHistory(t *testing.T, s standardRuntimeReceiptTestStore) {
	t.Helper()
	ins, _ := standardSnapshotFixture(t, s, "warm")
	r, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID)
	if err != nil || r.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || r.Binding.InstanceID != ins.ID || r.Check(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		t.Fatal("published runtime lost owned measured receipt", err)
	}
	original := r.Clone()
	r.ArtifactConsumption.Drives[0].Source.StorageKey = "reader-edit"
	again, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID)
	if err != nil || !again.Equal(original) {
		t.Fatal("history reader exposed mutable receipt", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled receipt read succeeded", err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown source exposed runtime history", err)
	}
	if err := s.UpdateInstanceStateWithTimestamp(t.Context(), ins.ID, string(StateSnapshotting), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID); err != nil || !got.Equal(original) {
		t.Fatal("snapshot state lost retained source receipt", err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("stopped source retained current residency", err)
	}
}

func standardRuntimeReceiptRestart(t *testing.T, s standardRuntimeReceiptTestStore) {
	t.Helper()
	ins, _ := standardSnapshotFixture(t, s, "warm")
	r, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := r.Binding
	restarted.Incarnation = uuid.NewString()
	registerConsumedNativeIdentity(t, s, restarted, runtimeadmission.ArtifactProtocolVersion)
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("restart retained stale residency proof", err)
	}
}

func TestMemStandardRuntimeReceiptOwnedHistory(t *testing.T) {
	standardRuntimeReceiptOwnedHistory(t, NewMemStore())
}
func TestMemStandardRuntimeReceiptRestart(t *testing.T) {
	standardRuntimeReceiptRestart(t, NewMemStore())
}
