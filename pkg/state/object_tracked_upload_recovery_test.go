package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrackedObjectUploadRecoveryMem(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	seed := func(phase string) state.ObjectUploadCompletion {
		c := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: uuid.NewString(), BucketID: uuid.NewString(), Key: "key", Status: "pending", WritePhase: phase, RecoveryRetryAt: time.Now().Add(-time.Second)}
		(*m.ObjectUploadCompletions())[c.ID] = c
		if (*m.ObjectWriteAdmissions()) == nil {
			(*m.ObjectWriteAdmissions()) = map[string]state.ObjectWriteAdmission{}
		}
		(*m.ObjectWriteAdmissions())[c.ID] = state.ObjectWriteAdmission{BucketID: c.BucketID, Route: true}
		return c
	}
	c := seed(state.ObjectUploadPrepared)
	claimed, err := m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "worker")
	if err != nil || claimed.Status != "failed" || claimed.ErrorCode != "preparation_expired" || !(*m.ObjectWriteAdmissions())[c.ID].Settled {
		t.Fatal(claimed, err)
	}
	if _, err = m.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late dispatcher escaped", err)
	}
	c = seed(state.ObjectUploadDispatched)
	claimed, err = m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "other"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("duplicate probe owner", err)
	}
	stale := claimed
	stale.RecoveryToken = "stale"
	stale.Status = "completed"
	stale.ETag = "etag"
	if _, err = m.FinishTrackedObjectUploadRecovery(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale confirmation", err)
	}
	claimed.RecoveryCursor = "private-cursor"
	claimed.RecoveryVersionsObserved = true
	oversized := claimed
	oversized.RecoveryCursor = strings.Repeat("x", api.ObjectUploadHistoryCursorMaxBytes+1)
	if err = m.RetryTrackedObjectUploadRecovery(ctx, oversized, "provider_write_uncertain"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unbounded cursor", err)
	}
	if err = m.RetryTrackedObjectUploadRecovery(ctx, claimed, "provider_write_uncertain"); err != nil {
		t.Fatal(err)
	}
	if (*m.ObjectWriteAdmissions())[c.ID].Settled {
		t.Fatal("absence settled an uncertain write")
	}
	claimed.RecoveryCursor = "stale-worker-cursor"
	if err = m.RetryTrackedObjectUploadRecovery(ctx, claimed, "provider_write_uncertain"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale cursor advance", err)
	}
	if _, err = m.FinishTrackedObjectUploadRecovery(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatal("released lease committed", err)
	}
	pending := (*m.ObjectUploadCompletions())[c.ID]
	if pending.RecoveryCursor != "private-cursor" || !pending.RecoveryVersionsObserved {
		t.Fatal("lost history progress", pending)
	}
	pending.RecoveryRetryAt = time.Now().Add(-time.Second)
	(*m.ObjectUploadCompletions())[c.ID] = pending
	claimed, err = m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	claimed.Status = "completed"
	claimed.ETag = "etag"
	claimed.ErrorCode = ""
	claimed.RecoveryVersionsObserved = false
	if _, err = m.FinishTrackedObjectUploadRecovery(ctx, claimed); err != nil || !(*m.ObjectWriteAdmissions())[c.ID].Settled {
		t.Fatal(err)
	}
	done := (*m.ObjectUploadCompletions())[c.ID]
	if done.RecoveryCursor != "" || !done.RecoveryVersionsObserved {
		t.Fatal("history latch lost", done)
	}
	if (*m.ObjectBuckets()) == nil {
		(*m.ObjectBuckets()) = map[string]state.ObjectBucket{}
	}
	(*m.ObjectBuckets())[c.BucketID] = state.ObjectBucket{ID: c.BucketID, AccountID: c.AccountID, State: "ready"}
	j, err := m.RequestObjectCapacityReconciliation(ctx, c.AccountID, "", c.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = m.ClaimObjectCapacityReconciliation(ctx, j.ID, "capacity-worker")
	if err != nil || j.State != "scanning" || j.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal("current inventory could refund versions", j, err)
	}
	// A late transport failure cannot rewrite a recovered completion.
	c.Status = "failed"
	c.ErrorCode = "provider_write_rejected"
	if _, err = m.FinishTrackedObjectUpload(ctx, c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late failure rewrote receipt", err)
	}
}
