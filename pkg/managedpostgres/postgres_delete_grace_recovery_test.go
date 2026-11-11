package managedpostgres

import (
	"testing"
	"time"
)

func TestPostgresPendingDeletionHoldSurvivesWorkerCrash(t *testing.T) {
	for _, undo := range []bool{false, true} {
		t.Run(map[bool]string{false: "schedule", true: "undo"}[undo], func(t *testing.T) {
			store, pool, ctx, account := postgresStoreFixture(t)
			now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
			database := postgresReadyDatabase(t, store, account, "crashed-hold", now.Add(-time.Hour))
			purgeAfter := now.Add(7 * 24 * time.Hour)
			claimed, err := store.ClaimScheduledDelete(ctx, account, database.ID, "schedule", now, now.Add(time.Minute), purgeAfter)
			if err != nil {
				t.Fatal(err)
			}
			if undo {
				if err := store.Release(ctx, database.ID, claimed.LeaseToken, StateDeleting, "", now, purgeAfter); err != nil {
					t.Fatal(err)
				}
				claimed, err = store.ClaimUndelete(ctx, account, database.ID, "undo", now, now.Add(time.Minute))
				if err != nil {
					t.Fatal(err)
				}
			}
			if claimed.LastErrorCode != deletionHoldPendingCode || !claimed.RetryAt.Equal(purgeAfter) {
				t.Fatalf("claim has no compatible recovery intent: %+v", claimed)
			}
			if due, err := store.Due(ctx, false, 100, claimed.LeaseUntil.Add(-time.Second)); err != nil || containsDatabase(due, database.ID) {
				t.Fatalf("live lease became due: %+v, %v", due, err)
			}
			if due, err := store.Due(ctx, false, 100, claimed.LeaseUntil); err != nil || !containsDatabase(due, database.ID) {
				t.Fatalf("expired lease lost its hold: %+v, %v", due, err)
			}
			// N-1 only discovers retry_at <= now. The same row must remain
			// invisible to it, since its reconciler would immediately purge.
			var legacyDue bool
			if err := pool.QueryRow(ctx, `SELECT retry_at <= $2 FROM managed_postgres_databases WHERE id=$1`, database.ID, claimed.LeaseUntil).Scan(&legacyDue); err != nil || legacyDue {
				t.Fatalf("legacy reconciler could purge early: due=%v err=%v", legacyDue, err)
			}
			if err := store.Release(ctx, database.ID, claimed.LeaseToken, StateDeleting, deletionHoldPendingCode, now, purgeAfter); err != nil {
				t.Fatal(err)
			}
			if due, err := store.Due(ctx, false, 100, now.Add(defaultPollInterval-time.Second)); err != nil || containsDatabase(due, database.ID) {
				t.Fatalf("hold retry did not back off: %+v, %v", due, err)
			}
			if due, err := store.Due(ctx, false, 100, now.Add(defaultPollInterval)); err != nil || !containsDatabase(due, database.ID) {
				t.Fatalf("hold retry did not resume: %+v, %v", due, err)
			}
		})
	}
}

func TestPostgresPendingDeletionHoldsRetryOldestAttemptFirst(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	first := postgresReadyDatabase(t, store, account, "first", now.Add(-time.Hour))
	second := postgresReadyDatabase(t, store, account, "second", now.Add(-time.Hour))
	purgeAfter := now.Add(7 * 24 * time.Hour)
	for i, database := range []Database{first, second} {
		at := now.Add(time.Duration(i) * time.Second)
		claimed, err := store.ClaimScheduledDelete(ctx, account, database.ID, "hold", at, at.Add(time.Minute), purgeAfter.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Release(ctx, database.ID, claimed.LeaseToken, StateDeleting, deletionHoldPendingCode, at, claimed.PurgeAfter); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)
	claimed, err := store.ClaimUndelete(ctx, account, first.ID, "retry", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, first.ID, claimed.LeaseToken, StateDeleting, deletionHoldPendingCode, now, purgeAfter); err != nil {
		t.Fatal(err)
	}
	due, err := store.Due(ctx, false, 1, now.Add(defaultPollInterval))
	if err != nil || len(due) != 1 || due[0].ID != second.ID {
		t.Fatalf("a failed earlier hold monopolized the batch: %+v, %v", due, err)
	}
}
