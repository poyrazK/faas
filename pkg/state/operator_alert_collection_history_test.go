package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperatorAlertCollectionHistoryStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := t.Context()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			f := state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now}
			empty, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || empty.Outcomes == nil || len(empty.Outcomes) != 0 {
				t.Fatalf("prospective: %+v %v", empty, err)
			}
			first := alertSnapshotFixture(now.Add(-3*time.Minute), "a", "b", now.Add(-4*time.Minute))
			mustAlertSnapshot(t, ctx, store, first)
			failed := now.Add(-2 * time.Minute)
			if err := store.RecordOperatorAlertFailure(ctx, failed, "timeout"); err != nil {
				t.Fatal(err)
			}
			// First receipt wins at equal time; delayed attempts cannot rewrite
			// outcomes or turn a recorded failure into successful collection.
			if err := store.RecordOperatorAlertFailure(ctx, failed, "source_read_failed"); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordOperatorAlertFailure(ctx, first.ObservedAt, "snapshot_rejected"); err != nil {
				t.Fatal(err)
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(failed, "a", "", time.Time{}))
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now, "a", "", time.Time{}))
			h, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(h.Outcomes) != 3 || h.Outcomes[0].Outcome != "success" || h.Outcomes[1].Reason != "timeout" || h.Outcomes[2].Outcome != "success" || !h.Collection.LastSuccessAt.Equal(now) {
				t.Fatalf("ordered immutable outcomes: %+v %v", h, err)
			}
			f.Since = failed.Add(30 * time.Second)
			h, err = store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(h.Outcomes) != 2 || !h.Outcomes[0].AttemptedAt.Equal(failed) {
				t.Fatalf("bounded carry-in: %+v %v", h, err)
			}
			if err := store.RecordOperatorAlertFailure(ctx, now.Add(time.Second), "private upstream error"); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("raw reason accepted: %v", err)
			}
			if err := store.RecordOperatorAlertFailure(ctx, now.Add(time.Second), "timeout", "timeout"); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("multiple reasons: %v", err)
			}
			if err := store.RecordOperatorAlertFailure(ctx, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			bounded, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(bounded.Outcomes) != 2 {
				t.Fatalf("future receipt entered history: %+v %v", bounded, err)
			}
			for _, bad := range []state.OperatorAlertCollectionHistoryFilter{{Now: now}, {Since: now, Now: now}, {Since: now.Add(-api.ObsAlertCollectionHistoryRetention - time.Microsecond), Now: now}} {
				if _, err := store.OperatorAlertCollectionHistory(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("invalid window: %v", err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.OperatorAlertCollectionHistory(canceled, f); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(time.Microsecond), 1); err != nil {
				t.Fatal(err)
			}
			h, err = store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(h.Outcomes) != 0 {
				t.Fatalf("pruned history: %+v %v", h, err)
			}
		})
	}
}

func TestOperatorAlertCollectionHistoryPostgresRollbackRestartAndBound(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := alertSnapshotFixture(now.Add(-time.Second), "a", "b", now.Add(-time.Minute))
	mustAlertSnapshot(t, ctx, store, first)
	// A storage constraint error after episode writes must roll everything
	// back, including resolution, status and its successful outcome receipt.
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_collection_outcomes ADD CONSTRAINT reject_test_success CHECK (outcome <> 'success') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(ctx, alertSnapshotFixture(now, "a", "", time.Time{})); err == nil {
		t.Fatal("failed receipt committed")
	}
	e, c, err := store.OperatorAlertEpisode(ctx, first.Episodes[0].ID)
	if err != nil || e.ResolvedAt != nil || !c.LastSuccessAt.Equal(first.ObservedAt) {
		t.Fatalf("partial commit: %+v %+v %v", e, c, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_collection_outcomes DROP CONSTRAINT reject_test_success"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertFailure(ctx, now, "persistence_failed"); err != nil {
		t.Fatal(err)
	}
	// A fresh Store connection sees the immutable receipts.
	restartPool, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restartPool.Close)
	restarted := state.NewPgStore(restartPool)
	f := state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now}
	h, err := restarted.OperatorAlertCollectionHistory(ctx, f)
	if err != nil || len(h.Outcomes) != 2 || h.Outcomes[1].Reason != "persistence_failed" {
		t.Fatalf("restart: %+v %v", h, err)
	}
	// Generate a high-rate retained history to exercise the hard read bound.
	if _, err := pool.Exec(ctx, "INSERT INTO operator_alert_collection_outcomes(attempted_at,outcome,reason) SELECT $1::timestamptz-i*interval '1 microsecond','failure','timeout' FROM generate_series(1,$2::integer) i", now, api.ObsAlertCollectionHistoryOutcomesMax); err != nil {
		t.Fatal(err)
	}
	_, err = store.OperatorAlertCollectionHistory(ctx, f)
	var bound *state.OperatorAlertCollectionHistoryLimitError
	if !errors.As(err, &bound) || bound.Limit != api.ObsAlertCollectionHistoryOutcomesMax {
		t.Fatalf("partial read accepted: %v", err)
	}
	for _, read := range []func() error{
		func() error {
			_, err := store.OperatorAlertRecurrence(ctx, state.OperatorAlertRecurrenceFilter{Since: f.Since, Now: now})
			return err
		},
		func() error {
			_, err := store.OperatorAlertResponseTimes(ctx, state.OperatorAlertResponseTimesFilter{Since: f.Since, Now: now})
			return err
		},
	} {
		if err := read(); !errors.As(err, &bound) {
			t.Fatalf("analytics hid incomplete coverage: %v", err)
		}
	}
}
