// adr: 20261023223000

package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectDeletionBusy pins H9-S156: concurrent deletes of different keys
// were refused as an undifferentiated conflict, which the gateway reported as
// a provider outage. Short-lived contention is now ErrObjectBucketBusy, so
// callers can wait for it, while ADR-547 still admits one deletion at a time.
func TestObjectDeletionBusyMem(t *testing.T) { objectDeletionBusySuite(t, state.NewMemStore()) }
func TestObjectDeletionBusyPG(t *testing.T) {
	st, _ := pgStore(t)
	objectDeletionBusySuite(t, st)
}

func objectDeletionBusySuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	d := st.(state.ObjectDeletionStore)
	first, created, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy())
	if err != nil || !created {
		t.Fatal(first, err)
	}
	other := deletionCaptureInput(b)
	other.Key = "other"
	if _, created, err = d.BeginObjectDeletion(ctx, other, accountingPolicy()); !errors.Is(err, state.ErrObjectBucketBusy) || !errors.Is(err, state.ErrConflict) || created {
		t.Fatal("second deletion was not busy", err)
	}
	// Writes keep their conflict: an uncertain deletion can fence for long.
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "new", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("write during a deletion", err)
	}

	// A tracked write in flight is also short-lived contention.
	w, _ := seedAccounting(t, st)
	if err = st.(state.ObjectCapacityStore).BeginObjectWrite(ctx, w.AccountID, w.ID, uuid.NewString(), "pending", 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.BeginObjectDeletion(ctx, deletionCaptureInput(w), accountingPolicy()); !errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("deletion beside a pending write was not busy", err)
	}

	// A configuration transition is a plain conflict, not worth waiting for.
	c, _ := seedAccounting(t, st)
	if _, err = st.(state.ObjectBucketVersioningStore).RequestObjectBucketVersioning(ctx, c.AccountID, c.AppID, c.ID, "Enabled"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.BeginObjectDeletion(ctx, deletionCaptureInput(c), accountingPolicy()); !errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("configuration fence reported as busy", err)
	}
}
