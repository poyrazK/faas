package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func rejectionFixture(at time.Time) api.ObsAlertCollectionRejection {
	ago, age := at.Add(-3*time.Minute), int64(180)
	return api.ObsAlertCollectionRejection{Scope: "rejected_snapshot", Reason: "rule_diagnostics_present", Totals: api.ObsRuleHealthTotals{Groups: 1, Rules: 1, Stale: 1}, Items: []api.ObsRuleHealthItem{{Kind: "rule", ID: strings.Repeat("a", 64), GroupID: strings.Repeat("b", 64), RuleName: "FaasApiDown", State: "stale", ReportedState: "inactive", Issues: []string{"evaluation_stale"}, LastEvaluatedAt: &ago, EvaluationAgeSeconds: &age}}, TotalItems: 1, Limit: api.ObsAlertCollectionRejectionItemsMax}
}

func TestOperatorAlertRejectionStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := t.Context()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			at := now.Add(-time.Minute)
			f := state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now, IncludeDetails: true}
			// Legacy reasons do not become new historical rule evidence.
			if err := store.RecordOperatorAlertFailure(ctx, at.Add(-time.Second), "snapshot_rejected"); err != nil {
				t.Fatal(err)
			}
			d := rejectionFixture(at)
			if err := store.RecordOperatorAlertRejection(ctx, at, d); err != nil {
				t.Fatal(err)
			}
			// Caller-owned slices and pointers cannot rewrite retained history.
			d.Items[0].RuleName = "MutatedName"
			d.Items[0].Issues[0] = "private_error"
			*d.Items[0].EvaluationAgeSeconds = 0
			// Equal/older writes cannot attach or replace evidence later.
			if err := store.RecordOperatorAlertRejection(ctx, at.Add(-time.Second), rejectionFixture(at.Add(-time.Second))); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordOperatorAlertFailure(ctx, at, "timeout"); err != nil {
				t.Fatal(err)
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now, "a", "", time.Time{}))
			h, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(h.Outcomes) != 3 || len(h.Failures) != 2 || h.Failures[0].DiagnosticsState != "recorded" || h.Failures[1].DiagnosticsState != "unknown" || h.Failures[1].Diagnostics != nil {
				t.Fatalf("history: %+v %v", h, err)
			}
			r := h.Failures[0].Diagnostics
			if r == nil || r.Items[0].RuleName != "FaasApiDown" || r.Items[0].Issues[0] != "evaluation_stale" || *r.Items[0].EvaluationAgeSeconds != 180 || !h.Collection.LastSuccessAt.Equal(now) {
				t.Fatalf("recovered history changed: %+v", r)
			}
			r.Items[0].Issues[0] = "mutated_return"
			again, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || again.Failures[0].Diagnostics.Items[0].Issues[0] != "evaluation_stale" {
				t.Fatalf("read aliases store: %+v %v", again, err)
			}
			f.IncludeDetails = false
			light, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || light.Failures != nil {
				t.Fatalf("unrequested payload: %+v %v", light, err)
			}
			f.IncludeDetails, f.Since = true, at.Add(time.Microsecond)
			carry, err := store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(carry.Outcomes) != 3 || len(carry.Failures) != 0 {
				t.Fatalf("carry-in counted as failed attempt: %+v %v", carry, err)
			}
			if err := store.RecordOperatorAlertRejection(ctx, now.Add(time.Second), d); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("unsafe input: %v", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := store.RecordOperatorAlertRejection(canceled, now.Add(time.Second), rejectionFixture(now.Add(time.Second))); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(time.Second), 1); err != nil {
				t.Fatal(err)
			}
			f.Since = now.Add(-time.Hour)
			h, err = store.OperatorAlertCollectionHistory(ctx, f)
			if err != nil || len(h.Failures) != 0 || len(h.Outcomes) != 0 {
				t.Fatalf("pruned diagnostics: %+v %v", h, err)
			}
		})
	}
}

func TestOperatorAlertRejectionPostgresRollbackAndRestart(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_collection_outcomes ADD CONSTRAINT reject_test_diagnostics CHECK (diagnostics IS NULL) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertRejection(ctx, now, rejectionFixture(now)); err == nil {
		t.Fatal("constraint failure committed")
	}
	f := state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now, IncludeDetails: true}
	h, err := store.OperatorAlertCollectionHistory(ctx, f)
	if err != nil || len(h.Outcomes) != 0 || len(h.Failures) != 0 || !h.Collection.LastAttemptAt.IsZero() {
		t.Fatalf("partial rejection commit: %+v %v", h, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_collection_outcomes DROP CONSTRAINT reject_test_diagnostics"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertRejection(ctx, now, rejectionFixture(now)); err != nil {
		t.Fatal(err)
	}
	restartPool, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restartPool.Close)
	h, err = state.NewPgStore(restartPool).OperatorAlertCollectionHistory(ctx, f)
	if err != nil || len(h.Failures) != 1 || h.Failures[0].Diagnostics == nil || h.Failures[0].Diagnostics.Items[0].RuleName != "FaasApiDown" {
		t.Fatalf("restart: %+v %v", h, err)
	}
	// A malformed persisted payload cannot be presented as trusted evidence.
	if _, err := pool.Exec(ctx, `UPDATE operator_alert_collection_outcomes SET diagnostics='{"scope":"rejected_snapshot","reason":"private_error"}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OperatorAlertCollectionHistory(ctx, f); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("corrupt evidence accepted: %v", err)
	}
	f.IncludeDetails = false
	if _, err := store.OperatorAlertCollectionHistory(ctx, f); err != nil {
		t.Fatalf("light history unnecessarily decoded details: %v", err)
	}
}

func TestOperatorAlertRejectionAttemptPrecision(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := t.Context()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			at := time.Now().UTC().Truncate(time.Microsecond)
			d := rejectionFixture(at)
			// Allowed clock skew can put an evaluation just after the stored
			// microsecond attempt. Its age must remain unknown on both stores.
			evaluated := at.Add(200 * time.Nanosecond)
			d.Totals.Stale, d.Totals.Failed = 0, 1
			d.Items[0].State, d.Items[0].Issues = "failed", []string{"evaluation_failed"}
			d.Items[0].LastEvaluatedAt, d.Items[0].EvaluationAgeSeconds = &evaluated, nil
			if err := store.RecordOperatorAlertRejection(ctx, at.Add(500*time.Nanosecond), d); err != nil {
				t.Fatal(err)
			}
			h, err := store.OperatorAlertCollectionHistory(ctx, state.OperatorAlertCollectionHistoryFilter{Since: at.Add(-time.Hour), Now: at, IncludeDetails: true})
			if err != nil || len(h.Failures) != 1 || !h.Failures[0].AttemptedAt.Equal(at) || h.Failures[0].Diagnostics == nil || h.Failures[0].Diagnostics.Items[0].EvaluationAgeSeconds != nil {
				t.Fatalf("attempt precision: %+v %v", h, err)
			}
		})
	}
}

func TestOperatorAlertRejectionPreviewStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := t.Context()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			for i := 0; i < api.ObsAlertCollectionFailureDetailsMax+3; i++ {
				at := now.Add(time.Duration(i-api.ObsAlertCollectionFailureDetailsMax-2) * time.Second)
				if err := store.RecordOperatorAlertRejection(ctx, at, rejectionFixture(at)); err != nil {
					t.Fatal(err)
				}
			}
			h, err := store.OperatorAlertCollectionHistory(ctx, state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now, IncludeDetails: true})
			if err != nil || len(h.Failures) != api.ObsAlertCollectionFailureDetailsMax || len(h.Outcomes) != api.ObsAlertCollectionFailureDetailsMax+3 || !h.Failures[0].AttemptedAt.Equal(now) {
				t.Fatalf("bounded complete history: %+v %v", h, err)
			}
		})
	}
}
