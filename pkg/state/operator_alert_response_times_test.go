package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func responseEpisode(t *testing.T, s state.OperatorAlertResponseTimesSnapshot, id string) state.OperatorAlertResponseTimesEpisode {
	t.Helper()
	for _, e := range s.Episodes {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("episode missing: %s", id)
	return state.OperatorAlertResponseTimesEpisode{}
}

// ADR-20261016110000000: receipt revisions establish first action; clocks do not.
func TestOperatorAlertResponseTimesStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			f := state.OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true}
			empty, err := store.OperatorAlertResponseTimes(ctx, f)
			if err != nil || empty.Episodes == nil || len(empty.Episodes) != 0 || !empty.Collection.LastSuccessAt.IsZero() {
				t.Fatalf("never: %+v %v", empty, err)
			}
			fixture := workloadFixture(now.Add(-20*time.Second), 5)
			starts := []time.Time{now.Add(-10 * time.Minute), now.Add(-5 * time.Minute), now.Add(-time.Minute), f.Since, f.Since.Add(-time.Microsecond)}
			for i := range fixture.Episodes {
				e := &fixture.Episodes[i]
				e.StartedAt = starts[i]
				e.Component = "apid"
				e.ID = state.OperatorAlertEpisodeID(e.RuleKey, e.SeriesID, e.StartedAt)
				fixture.Rules[0].EpisodeIDs[i] = e.ID
			}
			mustAlertSnapshot(t, ctx, store, fixture)
			mutate := func(i int, action string, revision int64, at time.Time) {
				t.Helper()
				if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: fixture.Episodes[i].ID, ActorID: triageActor, Action: action, ExpectedRevision: revision, Now: at}); err != nil {
					t.Fatal(err)
				}
			}
			for revision, action := range []string{"claim", "acknowledge", "unacknowledge", "acknowledge"} {
				mutate(0, action, int64(revision), starts[0].Add(time.Duration(revision+1)*10*time.Second))
			}
			mutate(1, "acknowledge", 0, starts[1]) // Zero is a valid duration, not a missing sample.
			mutate(2, "acknowledge", 0, now.Add(-10*time.Second))
			mutate(2, "claim", 1, now.Add(-20*time.Second))
			recovered := fixture
			recovered.Rules = append([]state.OperatorAlertRuleObservation{}, fixture.Rules...)
			recovered.ObservedAt = now.Add(-2 * time.Second)
			recovered.Rules[0].EvaluatedAt = recovered.ObservedAt
			recovered.Rules[0].EpisodeIDs = append([]string{}, fixture.Rules[0].EpisodeIDs[1:]...)
			recovered.Episodes = append([]state.OperatorAlertEpisode{}, fixture.Episodes[1:]...)
			mustAlertSnapshot(t, ctx, store, recovered)
			snap, err := store.OperatorAlertResponseTimes(ctx, f)
			if err != nil || len(snap.Episodes) != 4 {
				t.Fatalf("cohort: %+v %v", snap, err)
			}
			first := responseEpisode(t, snap, fixture.Episodes[0].ID)
			if first.FirstAcknowledgedAt == nil || !first.FirstAcknowledgedAt.Equal(starts[0].Add(20*time.Second)) || first.ReceiptCoverage != "complete" || first.AckClockInvalid || first.ResolvedAt == nil || first.Status != "resolved" {
				t.Fatalf("first acknowledgment: %+v", first)
			}
			if e := responseEpisode(t, snap, fixture.Episodes[1].ID); e.FirstAcknowledgedAt == nil || !e.FirstAcknowledgedAt.Equal(starts[1]) || e.Status != "firing" {
				t.Fatalf("zero: %+v", e)
			}
			if e := responseEpisode(t, snap, fixture.Episodes[2].ID); !e.AckClockInvalid || e.ReceiptCoverage != "complete" {
				t.Fatalf("backward clock: %+v", e)
			}
			if e := responseEpisode(t, snap, fixture.Episodes[3].ID); e.FirstAcknowledgedAt != nil || e.ReceiptCoverage != "complete" {
				t.Fatalf("unobserved: %+v", e)
			}
			f.CollectionEnabled = false
			disabled, err := store.OperatorAlertResponseTimes(ctx, f)
			if err != nil || responseEpisode(t, disabled, fixture.Episodes[1].ID).Status != "unknown" {
				t.Fatalf("disabled: %+v %v", disabled, err)
			}
			// Duplicate revisions make first-ack coverage unknown even if a plausible acknowledgment remains.
			logs, err := store.ListAuditLog(ctx, state.AuditLogFilter{KindPrefix: "operator.action.alert_triage", IncludeAnonymous: true, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range logs {
				var header struct {
					EpisodeID string `json:"episode_id"`
				}
				if err := json.Unmarshal(entry.Data, &header); err != nil {
					t.Fatal(err)
				}
				if header.EpisodeID == first.ID {
					entry.ID = uuid.New()
					if err := store.InsertAuditLog(ctx, entry); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			duplicate, err := store.OperatorAlertResponseTimes(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if e := responseEpisode(t, duplicate, first.ID); e.ReceiptCoverage != "invalid" || e.FirstAcknowledgedAt != nil {
				t.Fatalf("duplicate: %+v", e)
			}
			mutate(3, "acknowledge", 0, now.Add(time.Second))
			mutate(1, "unacknowledge", 1, starts[1].Add(-time.Microsecond))
			clocks, err := store.OperatorAlertResponseTimes(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if !responseEpisode(t, clocks, fixture.Episodes[3].ID).AckClockInvalid || !responseEpisode(t, clocks, fixture.Episodes[1].ID).AckClockInvalid {
				t.Fatalf("invalid timestamps became fast samples: %+v", clocks)
			}
			for _, bad := range []state.OperatorAlertResponseTimesFilter{{Now: now}, {Since: now, Now: now}, {Since: now.Add(-api.ObsAlertResponseTimesWindowMax - time.Microsecond), Now: now}} {
				if _, err := store.OperatorAlertResponseTimes(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("invalid: %v", err)
				}
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now.Add(time.Second), "c", "d", now.Add(time.Second)))
			bounded, err := store.OperatorAlertResponseTimes(ctx, f)
			if err != nil || len(bounded.Episodes) != 4 {
				t.Fatalf("future activation entered cohort: %+v %v", bounded, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.OperatorAlertResponseTimes(canceled, f); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
		})
	}
}

func TestOperatorAlertResponseTimesPostgresGapsAndRestart(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture := workloadFixture(now.Add(-time.Second), 1)
	mustAlertSnapshot(t, ctx, store, fixture)
	id := fixture.Episodes[0].ID
	for i, action := range []string{"claim", "acknowledge"} {
		if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: action, ExpectedRevision: int64(i), Now: fixture.Episodes[0].StartedAt.Add(time.Duration(i+1) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := store.ListAuditLog(ctx, state.AuditLogFilter{KindPrefix: "operator.action.alert_triage", IncludeAnonymous: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM audit_log WHERE kind='operator.action.alert_triage' AND data->>'episode_id'=$1 AND data#>>'{triage,revision}'='1'", id); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	f := state.OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true}
	gap, err := restarted.OperatorAlertResponseTimes(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if e := responseEpisode(t, gap, id); e.ReceiptCoverage != "gaps" || e.MissingRevisions != 1 || e.FirstAcknowledgedAt != nil {
		t.Fatalf("optimistic missing-first receipt: %+v", e)
	}
	for _, entry := range logs {
		var h struct {
			Previous int64 `json:"previous_revision"`
		}
		if err := json.Unmarshal(entry.Data, &h); err != nil {
			t.Fatal(err)
		}
		if h.Previous == 0 {
			if err := store.InsertAuditLog(ctx, entry); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE audit_log SET data=jsonb_set(data,'{action}','\"private invalid action\"'::jsonb) WHERE data->>'episode_id'=$1 AND data#>>'{triage,revision}'='2'", id); err != nil {
		t.Fatal(err)
	}
	invalid, err := restarted.OperatorAlertResponseTimes(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if e := responseEpisode(t, invalid, id); e.ReceiptCoverage != "invalid" || e.FirstAcknowledgedAt != nil {
		t.Fatalf("malformed receipt: %+v", e)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_log RENAME TO unavailable_audit_log"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.OperatorAlertResponseTimes(ctx, f); err == nil {
		t.Fatal("missing audit storage became empty timing")
	}
}

func TestOperatorAlertResponseTimesBounds(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			mustAlertSnapshot(t, ctx, store, workloadFixture(now, api.ObsAlertResponseTimesEpisodesMax+1))
			_, err := store.OperatorAlertResponseTimes(ctx, state.OperatorAlertResponseTimesFilter{Since: now.Add(-24 * time.Hour), Now: now, CollectionEnabled: true})
			var bound *state.OperatorAlertResponseTimesLimitError
			if !errors.As(err, &bound) || bound.Resource != "episodes" || bound.Limit != api.ObsAlertResponseTimesEpisodesMax {
				t.Fatalf("silent episode sampling: %v", err)
			}
		})
	}
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture := workloadFixture(now, 1)
	mustAlertSnapshot(t, ctx, store, fixture)
	if _, err := pool.Exec(ctx, "INSERT INTO audit_log(id,kind,data) SELECT md5(i::text)::uuid,'operator.action.alert_triage',jsonb_build_object('episode_id',$1::text) FROM generate_series(1,$2::integer) i", fixture.Episodes[0].ID, api.ObsAlertResponseTimesReceiptsMax+1); err != nil {
		t.Fatal(err)
	}
	_, err := store.OperatorAlertResponseTimes(ctx, state.OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true})
	var bound *state.OperatorAlertResponseTimesLimitError
	if !errors.As(err, &bound) || bound.Resource != "receipts" {
		t.Fatalf("silent receipt sampling: %v", err)
	}
}

func TestOperatorAlertResponseTimesConcurrentTriage(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			fixture := workloadFixture(now, 1)
			mustAlertSnapshot(t, ctx, store, fixture)
			id := fixture.Episodes[0].ID
			done := make(chan error, 1)
			go func() {
				for i := range 30 {
					if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: "acknowledge", ExpectedRevision: int64(i), Now: now}); err != nil {
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
				s, err := store.OperatorAlertResponseTimes(ctx, state.OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true})
				if err != nil || len(s.Episodes) != 1 || s.Episodes[0].ReceiptCoverage != "complete" || s.Episodes[0].MissingRevisions != 0 {
					t.Fatalf("torn receipt snapshot: %+v %v", s, err)
				}
			}
		})
	}
}

func TestOperatorAlertResponseTimesRecurrence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			first := alertSnapshotFixture(now.Add(-2*time.Second), "a", "b", now.Add(-time.Minute))
			mustAlertSnapshot(t, ctx, store, first)
			id := first.Episodes[0].ID
			if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: "acknowledge", Now: now.Add(-time.Second)}); err != nil {
				t.Fatal(err)
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now.Add(-time.Second), "a", "", time.Time{}))
			second := alertSnapshotFixture(now, "a", "b", now)
			mustAlertSnapshot(t, ctx, store, second)
			snap, err := store.OperatorAlertResponseTimes(ctx, state.OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true})
			if err != nil || len(snap.Episodes) != 2 {
				t.Fatalf("recurrence: %+v %v", snap, err)
			}
			old, fresh := responseEpisode(t, snap, id), responseEpisode(t, snap, second.Episodes[0].ID)
			if old.FirstAcknowledgedAt == nil || old.ResolvedAt == nil || fresh.FirstAcknowledgedAt != nil || fresh.Revision != 0 || fresh.ReceiptCoverage != "complete" {
				t.Fatalf("receipt identity crossed recurrence: %+v %+v", old, fresh)
			}
		})
	}
}
