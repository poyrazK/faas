package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261015154500000: cleanup receipts persist with deletion and report
// exact expiry independently of collection configuration or prior success.
func TestOperatorAlertRetentionStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			cutoff := now.Add(-api.ObsAlertHistoryRetention)
			initial, err := store.OperatorAlertRetentionStatus(ctx, now)
			if err != nil || !initial.LastAttemptAt.IsZero() || initial.ExpiredEpisodes != 0 {
				t.Fatalf("initial=%+v err=%v", initial, err)
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(cutoff.Add(-time.Minute), "a", "b", cutoff.Add(-2*time.Minute)))
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(cutoff, "c", "d", cutoff.Add(-time.Minute)))
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now, "e", "f", now.Add(-time.Minute)))
			before, err := store.OperatorAlertRetentionStatus(ctx, now)
			if err != nil || before.ExpiredEpisodes != 1 || !before.OldestExpiredAt.Equal(cutoff.Add(-time.Minute)) {
				t.Fatalf("before=%+v err=%v", before, err)
			}
			if n, err := store.PruneOperatorAlertHistory(ctx, cutoff, 1); err != nil || n != 1 {
				t.Fatalf("prune=%d err=%v", n, err)
			}
			after, err := store.OperatorAlertRetentionStatus(ctx, now)
			if err != nil || after.ExpiredEpisodes != 0 || !after.OldestExpiredAt.IsZero() || after.LastSuccessAt.IsZero() || !after.LastSuccessAt.Equal(after.LastAttemptAt) || after.LastPrunedEpisodes != 1 {
				t.Fatalf("after=%+v err=%v", after, err)
			}
			// Strict cutoff: the boundary episode becomes eligible only later.
			boundary, err := store.OperatorAlertRetentionStatus(ctx, now.Add(time.Microsecond))
			if err != nil || boundary.ExpiredEpisodes != 1 || !boundary.OldestExpiredAt.Equal(cutoff) {
				t.Fatalf("boundary=%+v err=%v", boundary, err)
			}
			failedAt := time.Now().UTC().Truncate(time.Microsecond)
			if err := store.RecordOperatorAlertRetentionFailure(ctx, failedAt); err != nil {
				t.Fatal(err)
			}
			failed, err := store.OperatorAlertRetentionStatus(ctx, now)
			if err != nil || !failed.LastAttemptAt.Equal(failedAt) || !failed.LastSuccessAt.Equal(after.LastSuccessAt) || failed.LastPrunedEpisodes != 1 {
				t.Fatalf("failed=%+v err=%v", failed, err)
			}
			if err := store.RecordOperatorAlertRetentionFailure(ctx, after.LastSuccessAt.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			delayed, err := store.OperatorAlertRetentionStatus(ctx, now)
			if err != nil || !delayed.LastAttemptAt.Equal(failedAt) {
				t.Fatalf("delayed clobbered failure: %+v %v", delayed, err)
			}
			// Failure and cleanup receipts use microsecond precision. Make the
			// later recovery step strictly newer even on the fast memory path.
			for !time.Now().UTC().Truncate(time.Microsecond).After(failedAt) {
				time.Sleep(time.Microsecond)
			}
			if _, err := store.PruneOperatorAlertHistory(ctx, cutoff, 1); err != nil {
				t.Fatal(err)
			}
			recovered, err := store.OperatorAlertRetentionStatus(ctx, now)
			if err != nil || recovered.LastSuccessAt.Before(failedAt) || recovered.LastPrunedEpisodes != 0 {
				t.Fatalf("recovered=%+v err=%v", recovered, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.OperatorAlertRetentionStatus(cancelled, now); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled read=%v", err)
			}
			if err := store.RecordOperatorAlertRetentionFailure(cancelled, now); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled failure write=%v", err)
			}
			if err := store.RecordOperatorAlertRetentionFailure(ctx, time.Time{}); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("zero failure time=%v", err)
			}
			if _, err := store.OperatorAlertRetentionStatus(ctx, time.Time{}); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("zero read time=%v", err)
			}
		})
	}
}

func TestOperatorAlertRetentionReceiptFailureRollsBackDeletion(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC()
	old := now.Add(-api.ObsAlertHistoryRetention - time.Hour)
	mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(old, "a", "b", old.Add(-time.Minute)))
	// Force the success receipt to fail after DELETE, proving one transaction.
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_retention_health ADD CONSTRAINT reject_cleanup_receipt CHECK (last_success_at IS NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(-api.ObsAlertHistoryRetention), 1); err == nil {
		t.Fatal("receipt failure accepted")
	}
	after, err := store.OperatorAlertRetentionStatus(ctx, now)
	if err != nil || after.ExpiredEpisodes != 1 || !after.LastSuccessAt.IsZero() {
		t.Fatalf("partial deletion: %+v %v", after, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_retention_health DROP CONSTRAINT reject_cleanup_receipt"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(-api.ObsAlertHistoryRetention), 1); err != nil {
		t.Fatal(err)
	}
	reopened := state.NewPgStore(pool)
	receipt, err := reopened.OperatorAlertRetentionStatus(ctx, now)
	if err != nil || receipt.LastPrunedEpisodes != 1 || receipt.LastSuccessAt.IsZero() || receipt.ExpiredEpisodes != 0 {
		t.Fatalf("receipt not durable: %+v %v", receipt, err)
	}
}

func TestOperatorAlertRetentionMissingReceiptPreventsDeletion(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC()
	old := now.Add(-api.ObsAlertHistoryRetention - time.Hour)
	snapshot := alertSnapshotFixture(old, "a", "b", old.Add(-time.Minute))
	mustAlertSnapshot(t, ctx, store, snapshot)
	if _, err := pool.Exec(ctx, "DELETE FROM operator_alert_retention_health"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(-api.ObsAlertHistoryRetention), 1); err == nil {
		t.Fatal("cleanup without a receipt row succeeded")
	}
	episode, _, err := store.OperatorAlertEpisode(ctx, snapshot.Episodes[0].ID)
	if err != nil || episode.ID != snapshot.Episodes[0].ID {
		t.Fatalf("history deleted without a receipt: %+v %v", episode, err)
	}
}
