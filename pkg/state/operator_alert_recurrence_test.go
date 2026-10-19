package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperatorAlertRecurrenceStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := t.Context()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			f := state.OperatorAlertRecurrenceFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true}
			empty, err := store.OperatorAlertRecurrence(ctx, f)
			if err != nil || empty.Episodes == nil || len(empty.Episodes) != 0 || !empty.Collection.LastSuccessAt.IsZero() {
				t.Fatalf("never: %+v %v", empty, err)
			}
			first := alertSnapshotFixture(now.Add(-5*time.Minute), "a", "b", f.Since)
			mustAlertSnapshot(t, ctx, store, first)
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now.Add(-4*time.Minute), "a", "", time.Time{}))
			second := alertSnapshotFixture(now.Add(-time.Minute), "a", "b", now.Add(-2*time.Minute))
			mustAlertSnapshot(t, ctx, store, second)
			// A changed rule with the same series stays separate; a missing old
			// rule leaves the unresolved episode unknown, never recovered.
			changed := alertSnapshotFixture(now, "c", "b", now)
			mustAlertSnapshot(t, ctx, store, changed)
			snap, err := store.OperatorAlertRecurrence(ctx, f)
			if err != nil || len(snap.Episodes) != 3 || snap.Episodes[0].ID != changed.Episodes[0].ID || snap.Episodes[0].Status != "firing" || snap.Episodes[1].ID != second.Episodes[0].ID || snap.Episodes[1].Status != "unknown" || snap.Episodes[2].ResolvedAt == nil || snap.Episodes[2].Status != "resolved" || !snap.Episodes[2].StartedAt.Equal(f.Since) {
				t.Fatalf("retained identity and inclusive window: %+v %v", snap, err)
			}
			if snap.Episodes[0].RuleKey == snap.Episodes[1].RuleKey || snap.Episodes[0].SeriesID != snap.Episodes[1].SeriesID || snap.Episodes[1].ID == snap.Episodes[2].ID {
				t.Fatal("rule or activation identity lost")
			}
			*snap.Episodes[2].ResolvedAt = now
			again, err := store.OperatorAlertRecurrence(ctx, f)
			if err != nil || !again.Episodes[2].ResolvedAt.Equal(now.Add(-4*time.Minute)) {
				t.Fatalf("resolution alias: %+v %v", again, err)
			}
			f.CollectionEnabled = false
			disabled, err := store.OperatorAlertRecurrence(ctx, f)
			if err != nil || disabled.Episodes[0].Status != "unknown" || disabled.Episodes[2].Status != "resolved" {
				t.Fatalf("disabled: %+v %v", disabled, err)
			}
			f.Since = f.Since.Add(time.Microsecond)
			narrow, err := store.OperatorAlertRecurrence(ctx, f)
			if err != nil || len(narrow.Episodes) != 2 {
				t.Fatalf("lower boundary: %+v %v", narrow, err)
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now.Add(time.Second), "d", "e", now.Add(time.Second)))
			future, err := store.OperatorAlertRecurrence(ctx, f)
			if err != nil || len(future.Episodes) != 2 {
				t.Fatalf("future activation entered cohort: %+v %v", future, err)
			}
			for _, bad := range []state.OperatorAlertRecurrenceFilter{{Now: now}, {Since: now, Now: now}, {Since: now.Add(-api.ObsAlertRecurrenceWindowMax - time.Microsecond), Now: now}} {
				if _, err := store.OperatorAlertRecurrence(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("bad filter: %v", err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.OperatorAlertRecurrence(canceled, f); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled: %v", err)
			}
		})
	}
}

func TestOperatorAlertRecurrenceBound(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := t.Context()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			mustAlertSnapshot(t, ctx, store, workloadFixture(now, api.ObsAlertRecurrenceEpisodesMax+1))
			_, err := store.OperatorAlertRecurrence(ctx, state.OperatorAlertRecurrenceFilter{Since: now.Add(-24 * time.Hour), Now: now, CollectionEnabled: true})
			var bound *state.OperatorAlertRecurrenceLimitError
			if !errors.As(err, &bound) || bound.Limit != api.ObsAlertRecurrenceEpisodesMax {
				t.Fatalf("partial cohort returned: %v", err)
			}
			bounded, err := store.OperatorAlertRecurrence(ctx, state.OperatorAlertRecurrenceFilter{Since: now.Add(-time.Duration(api.ObsAlertRecurrenceEpisodesMax) * time.Minute), Now: now, CollectionEnabled: true})
			if err != nil || len(bounded.Episodes) != api.ObsAlertRecurrenceEpisodesMax {
				t.Fatalf("exact bound rejected: count=%d err=%v", len(bounded.Episodes), err)
			}
		})
	}
}
