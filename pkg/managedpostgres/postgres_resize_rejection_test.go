package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresRejectedResizeSettlesSourceAndReleasesPins(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	database := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(database)
	if _, err := store.ReserveResize(ctx, database, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, err := store.Claim(ctx, account, database.ID, "rejected", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ready, err := store.FailResize(ctx, claim, op, "invalid", time.Now())
	if err != nil || ready.State != StateReady || ready.Spec != database.Spec || ready.ObservedGeneration != op.Generation || ready.LeaseToken != "" {
		t.Fatalf("rejection did not release the source: database=%+v err=%v", ready, err)
	}
	receipt, err := store.GetResize(ctx, account, op.ID)
	if err != nil || receipt.State != ResizeFailed || receipt.ErrorCode != "invalid" || receipt.LastErrorCode != "invalid" || receipt.CompletedAt.IsZero() {
		t.Fatalf("failed receipt lost: %+v err=%v", receipt, err)
	}
	if _, err := store.FailResize(ctx, claim, op, "invalid", time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale failure claim changed a terminal receipt", err)
	}
	if _, err := store.ClaimDelete(ctx, account, database.ID, "purge", time.Now(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal("rejected resize still blocked purge", err)
	}
}

func TestPostgresRejectedResizeAllowsCorrectedRequest(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	database := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(database)
	if _, err := store.ReserveResize(ctx, database, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, account, database.ID, "rejected", StateUpdating, time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ready, err := store.FailResize(ctx, claim, op, "invalid", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	corrected := postgresResizeIntent(ready)
	if next, err := store.ReserveResize(ctx, ready, corrected, time.Now()); err != nil || next.Generation != op.Generation+1 {
		t.Fatalf("corrected resize blocked: %+v err=%v", next, err)
	}
}
