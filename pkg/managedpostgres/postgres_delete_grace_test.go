// adr: 20261029174950 — the catalog holds a scheduled delete's window, lets an
// undo claim it only while it is open, and an immediate delete ends it.
package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresStoreScheduledDelete(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	start := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	purgeAfter := start.Add(7 * 24 * time.Hour)
	database := postgresReadyDatabase(t, store, accountID, "grace", start)
	at := start.Add(time.Minute)

	claimed, err := store.ClaimScheduledDelete(ctx, accountID, database.ID, "schedule", at, at.Add(time.Minute), purgeAfter)
	if err != nil || claimed.State != StateDeleting || !claimed.PurgeAfter.Equal(purgeAfter) || !claimed.RetryAt.Equal(purgeAfter) {
		t.Fatalf("ClaimScheduledDelete = %+v, %v", claimed, err)
	}
	if _, err := store.ClaimUndelete(ctx, accountID, database.ID, "undo", at, at.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("undo while the schedule is leased = %v, want ErrConflict", err)
	}
	if err := store.Release(ctx, database.ID, "schedule", StateDeleting, "", at, purgeAfter); err != nil {
		t.Fatal(err)
	}
	if due, err := store.Due(ctx, true, 100, purgeAfter.Add(-time.Second)); err != nil || containsDatabase(due, database.ID) {
		t.Fatalf("due inside the window: %v", err)
	}
	if due, err := store.Due(ctx, true, 100, purgeAfter); err != nil || !containsDatabase(due, database.ID) {
		t.Fatalf("not due when the window ends: %v", err)
	}

	undo, err := store.ClaimUndelete(ctx, accountID, database.ID, "undo", at, at.Add(time.Minute))
	if err != nil || undo.LeaseToken != "undo" {
		t.Fatalf("ClaimUndelete = %+v, %v", undo, err)
	}
	ready, err := store.CancelScheduledDelete(ctx, database.ID, "undo", at)
	if err != nil || ready.State != StateReady || !ready.PurgeAfter.IsZero() || ready.LeaseToken != "" {
		t.Fatalf("CancelScheduledDelete = %+v, %v", ready, err)
	}
	if _, err := store.ClaimUndelete(ctx, accountID, database.ID, "undo-again", at, at.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("undo of a ready database = %v, want ErrConflict", err)
	}

	// After the window, or once an immediate delete starts, undo is gone.
	if _, err := store.ClaimScheduledDelete(ctx, accountID, database.ID, "schedule-2", at, at.Add(time.Minute), purgeAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, database.ID, "schedule-2", StateDeleting, "", at, purgeAfter); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimUndelete(ctx, accountID, database.ID, "late", purgeAfter, purgeAfter.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undo when the window ended = %v, want ErrNotFound", err)
	}
	purging, err := store.ClaimDelete(ctx, accountID, database.ID, "purge", at, at.Add(time.Minute))
	if err != nil || !purging.PurgeAfter.IsZero() {
		t.Fatalf("an immediate delete kept the window: %+v, %v", purging, err)
	}
	if err := store.Release(ctx, database.ID, "purge", StateDeleting, "provider_unavailable", at, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimUndelete(ctx, accountID, database.ID, "undo-purge", at, at.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undo during a purge retry = %v, want ErrNotFound", err)
	}

	// A binding blocks the schedule as it blocks an immediate delete.
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: accountID, Slug: "grace-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	bound := postgresReadyDatabase(t, store, accountID, "grace-bound", start)
	if _, created, err := store.ReserveBinding(ctx, testBinding(accountID, bound.ID, app.ID, uuid.NewString(), at)); err != nil || !created {
		t.Fatalf("reserve binding: %v %v", created, err)
	}
	if _, err := store.ClaimScheduledDelete(ctx, accountID, bound.ID, "bound", at, at.Add(time.Minute), purgeAfter); !errors.Is(err, ErrConflict) {
		t.Fatalf("schedule with a binding = %v, want ErrConflict", err)
	}
}

func containsDatabase(databases []Database, id string) bool {
	for _, database := range databases {
		if database.ID == id {
			return true
		}
	}
	return false
}
