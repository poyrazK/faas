package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261016120000000: workload aggregation covers all retained rows before pagination.
func workloadFixture(now time.Time, n int) state.OperatorAlertSnapshot {
	s := alertSnapshotFixture(now, "a", "", time.Time{})
	for i := range n {
		e := state.OperatorAlertEpisode{RuleKey: s.Rules[0].Key, SeriesID: fmt.Sprintf("%064x", i+1), Name: "FaasApiDown", Severity: "warning", StartedAt: now.Add(-time.Duration(n-i) * time.Minute)}
		if i == 0 {
			e.Severity = "critical"
		}
		if i == 1 {
			e.Severity = "error"
		}
		e.ID = state.OperatorAlertEpisodeID(e.RuleKey, e.SeriesID, e.StartedAt)
		s.Episodes = append(s.Episodes, e)
		s.Rules[0].EpisodeIDs = append(s.Rules[0].EpisodeIDs, e.ID)
	}
	return s
}

func workloadCount(s state.OperatorAlertWorkloadSnapshot, status string) int64 {
	var n int64
	for _, b := range s.Buckets {
		if status == "" || b.Status == status {
			n += b.Count
		}
	}
	return n
}

func TestOperatorAlertWorkloadStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			f := state.OperatorAlertWorkloadFilter{Now: now, CollectionEnabled: true}
			empty, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || empty.Buckets == nil || len(empty.Buckets) != 0 || !empty.Collection.LastSuccessAt.IsZero() {
				t.Fatalf("never collected: %+v %v", empty, err)
			}
			old := workloadFixture(now.Add(-40*24*time.Hour), 1)
			mustAlertSnapshot(t, ctx, store, old)
			resolved := old
			resolved.ObservedAt = old.ObservedAt.Add(time.Second)
			resolved.Rules = append([]state.OperatorAlertRuleObservation{}, old.Rules...)
			resolved.Rules[0].EvaluatedAt = resolved.ObservedAt
			resolved.Rules[0].EpisodeIDs = []string{}
			resolved.Episodes = []state.OperatorAlertEpisode{}
			mustAlertSnapshot(t, ctx, store, resolved)
			current := workloadFixture(now, 205)
			mustAlertSnapshot(t, ctx, store, current)
			mutate := func(index int, action string, revision int64) {
				t.Helper()
				m := state.OperatorAlertTriageMutation{EpisodeID: current.Episodes[index].ID, ActorID: triageActor, Action: action, ExpectedRevision: revision, Now: now}
				if action == "snooze" {
					until := now.Add(time.Minute)
					m.SnoozedUntil = &until
				}
				if _, err := store.MutateOperatorAlertTriage(ctx, m); err != nil {
					t.Fatal(err)
				}
			}
			mutate(1, "acknowledge", 0)
			mutate(1, "claim", 1)
			mutate(2, "snooze", 0)
			mutate(3, "acknowledge", 0)
			mutate(4, "claim", 0)
			snap, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(snap, "") != 206 || workloadCount(snap, "firing") != 205 || workloadCount(snap, "resolved") != 1 || snap.Retention.ExpiredEpisodes != 1 || len(snap.Buckets) > api.ObsAlertWorkloadGroupMax {
				t.Fatalf("exact counts: %+v %v", snap, err)
			}
			var attention int64
			for _, b := range snap.Buckets {
				if b.Status != "resolved" && !b.Snoozed && (!b.Acknowledged || !b.Owned) {
					attention += b.Count
				}
			}
			if attention != 203 {
				t.Fatalf("attention=%d", attention)
			}
			f.Now = now.Add(time.Minute)
			expired, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(expired, "firing") != 205 {
				t.Fatalf("expiry: %+v %v", expired, err)
			}
			for _, b := range expired.Buckets {
				if b.Snoozed {
					t.Fatal("snooze remains active at deadline")
				}
			}
			e, _, err := store.OperatorAlertEpisode(ctx, current.Episodes[2].ID)
			if err != nil || e.Triage.Revision != 1 {
				t.Fatal("summary mutated workflow")
			}
			f.Now = now
			f.CollectionEnabled = false
			disabled, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(disabled, "unknown") != 205 || workloadCount(disabled, "firing") != 0 || workloadCount(disabled, "resolved") != 1 {
				t.Fatalf("disabled: %+v %v", disabled, err)
			}
			f.CollectionEnabled = true
			f.Now = now.Add(api.ObsHealthFreshnessMaxAge + time.Microsecond)
			stale, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(stale, "unknown") != 205 {
				t.Fatalf("stale: %+v %v", stale, err)
			}
			if err := store.RecordOperatorAlertFailure(ctx, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			f.Now = now.Add(time.Second)
			failed, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(failed, "unknown") != 205 {
				t.Fatalf("failed collector: %+v %v", failed, err)
			}
			// A fresh collector can still leave individual missing rules unknown.
			other := alertSnapshotFixture(now.Add(2*time.Second), "c", "", time.Time{})
			mustAlertSnapshot(t, ctx, store, other)
			f.Now = other.ObservedAt
			missingRule, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(missingRule, "unknown") != 205 || !missingRule.Collection.LastSuccessAt.Equal(other.ObservedAt) {
				t.Fatalf("missing rule: %+v %v", missingRule, err)
			}
			// Recurrence creates a fresh unacknowledged/unowned episode; the old episode remains resolved.
			recovery := workloadFixture(now.Add(3*time.Second), 0)
			mustAlertSnapshot(t, ctx, store, recovery)
			recurrence := workloadFixture(now.Add(4*time.Second), 1)
			mustAlertSnapshot(t, ctx, store, recurrence)
			f.Now = recurrence.ObservedAt
			next, err := store.OperatorAlertWorkload(ctx, f)
			if err != nil || workloadCount(next, "firing") != 1 || workloadCount(next, "resolved") != 206 || workloadCount(next, "") != 207 {
				t.Fatalf("recurrence: %+v %v", next, err)
			}
			for _, b := range next.Buckets {
				if b.Status == "firing" && (b.Acknowledged || b.Owned || b.Snoozed) {
					t.Fatal("recurrence inherited triage")
				}
			}
			if _, err := store.OperatorAlertWorkload(ctx, state.OperatorAlertWorkloadFilter{}); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("zero clock: %v", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.OperatorAlertWorkload(canceled, f); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
		})
	}
}

func TestOperatorAlertWorkloadConcurrentRetention(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			mustAlertSnapshot(t, ctx, store, workloadFixture(now.Add(-40*24*time.Hour), 20))
			done := make(chan error, 1)
			go func() {
				for range 20 {
					if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(-api.ObsAlertHistoryRetention), 1); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			t.Cleanup(func() {
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			for range 20 {
				snap, err := store.OperatorAlertWorkload(ctx, state.OperatorAlertWorkloadFilter{Now: now, CollectionEnabled: true})
				if err != nil || workloadCount(snap, "") != snap.Retention.ExpiredEpisodes {
					t.Fatalf("torn cleanup snapshot: %+v %v", snap, err)
				}
			}
		})
	}
}

func TestOperatorAlertWorkloadPostgresStorageFailure(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	mustAlertSnapshot(t, ctx, store, workloadFixture(now, 1))
	if _, err := pool.Exec(ctx, "ALTER TABLE operator_alert_episodes RENAME TO unavailable_operator_alert_episodes"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.NewPgStore(pool).OperatorAlertWorkload(ctx, state.OperatorAlertWorkloadFilter{Now: now, CollectionEnabled: true}); err == nil {
		t.Fatal("storage failure became empty workload")
	}
}

func TestOperatorAlertWorkloadConcurrentTriageAndOldestTie(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			fixture := workloadFixture(now, 2)
			for i := range fixture.Episodes {
				fixture.Episodes[i].Severity = "warning"
				fixture.Episodes[i].StartedAt = now.Add(-time.Hour)
				fixture.Episodes[i].ID = state.OperatorAlertEpisodeID(fixture.Episodes[i].RuleKey, fixture.Episodes[i].SeriesID, fixture.Episodes[i].StartedAt)
				fixture.Rules[0].EpisodeIDs[i] = fixture.Episodes[i].ID
			}
			mustAlertSnapshot(t, ctx, store, fixture)
			f := state.OperatorAlertWorkloadFilter{Now: now, CollectionEnabled: true}
			snap, err := store.OperatorAlertWorkload(ctx, f)
			want := min(fixture.Episodes[0].ID, fixture.Episodes[1].ID)
			if err != nil || len(snap.Buckets) != 1 || snap.Buckets[0].Count != 2 || snap.Buckets[0].OldestID != want {
				t.Fatalf("oldest tie: %+v %v", snap, err)
			}
			done := make(chan error, 1)
			go func() {
				for i := range 30 {
					action := "acknowledge"
					if i%2 == 1 {
						action = "unacknowledge"
					}
					if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: fixture.Episodes[0].ID, ActorID: triageActor, Action: action, ExpectedRevision: int64(i), Now: now}); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			t.Cleanup(func() {
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			for range 30 {
				page, err := store.OperatorAlertWorkload(ctx, f)
				if err != nil || workloadCount(page, "") != 2 || len(page.Buckets) > 2 {
					t.Fatalf("torn triage groups: %+v %v", page, err)
				}
			}
		})
	}
}
