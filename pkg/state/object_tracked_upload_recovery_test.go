package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTrackedObjectUploadRecoveryMem(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	seed := func(phase string) ObjectUploadCompletion {
		c := ObjectUploadCompletion{ID: uuid.NewString(), AccountID: uuid.NewString(), BucketID: uuid.NewString(), Key: "key", Status: "pending", WritePhase: phase, RecoveryRetryAt: time.Now().Add(-time.Second)}
		m.objectUploadCompletions[c.ID] = c
		if m.objectWriteAdmissions == nil {
			m.objectWriteAdmissions = map[string]objectWriteAdmission{}
		}
		m.objectWriteAdmissions[c.ID] = objectWriteAdmission{BucketID: c.BucketID, Route: true}
		return c
	}
	c := seed(ObjectUploadPrepared)
	claimed, err := m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "worker")
	if err != nil || claimed.Status != "failed" || claimed.ErrorCode != "preparation_expired" || !m.objectWriteAdmissions[c.ID].Settled {
		t.Fatal(claimed, err)
	}
	if _, err = m.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("late dispatcher escaped", err)
	}
	c = seed(ObjectUploadDispatched)
	claimed, err = m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "other"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate probe owner", err)
	}
	stale := claimed
	stale.RecoveryToken = "stale"
	stale.Status = "completed"
	stale.ETag = "etag"
	if _, err = m.FinishTrackedObjectUploadRecovery(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale confirmation", err)
	}
	if err = m.RetryTrackedObjectUploadRecovery(ctx, claimed, "provider_write_uncertain"); err != nil {
		t.Fatal(err)
	}
	if m.objectWriteAdmissions[c.ID].Settled {
		t.Fatal("absence settled an uncertain write")
	}
	if _, err = m.FinishTrackedObjectUploadRecovery(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatal("released lease committed", err)
	}
	pending := m.objectUploadCompletions[c.ID]
	pending.RecoveryRetryAt = time.Now().Add(-time.Second)
	m.objectUploadCompletions[c.ID] = pending
	claimed, err = m.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	claimed.Status = "completed"
	claimed.ETag = "etag"
	claimed.ErrorCode = ""
	if _, err = m.FinishTrackedObjectUploadRecovery(ctx, claimed); err != nil || !m.objectWriteAdmissions[c.ID].Settled {
		t.Fatal(err)
	}
	// A late transport failure cannot rewrite a recovered completion.
	c.Status = "failed"
	c.ErrorCode = "provider_write_rejected"
	if _, err = m.FinishTrackedObjectUpload(ctx, c); !errors.Is(err, ErrConflict) {
		t.Fatal("late failure rewrote receipt", err)
	}
}
