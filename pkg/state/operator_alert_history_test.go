package state_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func alertSnapshotFixture(now time.Time, rule, series string, started time.Time) state.OperatorAlertSnapshot {
	ruleKey := strings.Repeat(rule, 64)
	s := state.OperatorAlertSnapshot{ObservedAt: now, Rules: []state.OperatorAlertRuleObservation{{Key: ruleKey, EvaluatedAt: now, EpisodeIDs: []string{}}}, Episodes: []state.OperatorAlertEpisode{}}
	if series != "" {
		e := state.OperatorAlertEpisode{RuleKey: ruleKey, SeriesID: strings.Repeat(series, 64), Name: "FaasApiDown", Component: "apid", Severity: "critical", RunbookURL: "/docs/runbooks/FaasApiDown.md", StartedAt: started}
		e.ID = state.OperatorAlertEpisodeID(e.RuleKey, e.SeriesID, started)
		s.Episodes = append(s.Episodes, e)
		s.Rules[0].EpisodeIDs = append(s.Rules[0].EpisodeIDs, e.ID)
	}
	return s
}

func TestOperatorAlertHistoryStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			filter := state.OperatorAlertFilter{Now: now, Since: now.Add(-24 * time.Hour), Limit: 201}
			page, err := store.OperatorAlertEpisodes(ctx, filter)
			if err != nil || page.Episodes == nil || len(page.Episodes) != 0 || page.Collection.Health(now).Reason != "not_collected" {
				t.Fatalf("empty=%+v err=%v", page, err)
			}
			missing := strings.Repeat("f", 64)
			if _, _, err := store.OperatorAlertEpisode(ctx, missing); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			first := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
			id := first.Episodes[0].ID
			mustAlertSnapshot(t, ctx, store, first)
			e, c, err := store.OperatorAlertEpisode(ctx, id)
			if err != nil || e.Status(c, now) != "firing" || !e.FirstSeenAt.Equal(now) || !e.LastSeenAt.Equal(now) {
				t.Fatalf("first=%+v collection=%+v err=%v", e, c, err)
			}
			duplicate := first
			duplicate.ObservedAt = now.Add(time.Second)
			mustAlertSnapshot(t, ctx, store, duplicate)
			e, c, err = store.OperatorAlertEpisode(ctx, id)
			if err != nil || !e.FirstSeenAt.Equal(now) || !e.LastSeenAt.Equal(duplicate.ObservedAt) || e.Status(c, duplicate.ObservedAt) != "firing" {
				t.Fatalf("duplicate=%+v err=%v", e, err)
			}
			// Same evaluation with a contradictory empty set is never a recovery.
			contradiction := alertSnapshotFixture(now.Add(2*time.Second), "a", "", time.Time{})
			contradiction.Rules[0].EvaluatedAt = now
			mustAlertSnapshot(t, ctx, store, contradiction)
			e, c, _ = store.OperatorAlertEpisode(ctx, id)
			if e.ResolvedAt != nil || e.Status(c, contradiction.ObservedAt) != "unknown" {
				t.Fatalf("contradictory duplicate resolved alert: %+v", e)
			}
			// A missing rule cannot resolve an existing episode, even with other fresh rules.
			other := alertSnapshotFixture(now.Add(3*time.Second), "c", "d", now.Add(-time.Minute))
			mustAlertSnapshot(t, ctx, store, other)
			e, c, _ = store.OperatorAlertEpisode(ctx, id)
			if e.ResolvedAt != nil || e.Status(c, other.ObservedAt) != "unknown" {
				t.Fatalf("missing rule resolved alert: %+v", e)
			}
			failureAt := now.Add(4 * time.Second)
			if err := store.RecordOperatorAlertFailure(ctx, failureAt); err != nil {
				t.Fatal(err)
			}
			e, c, _ = store.OperatorAlertEpisode(ctx, id)
			if e.ResolvedAt != nil || c.Health(failureAt).Reason != "collection_failed" {
				t.Fatalf("failed source: %+v %+v", e, c)
			}
			recovered := alertSnapshotFixture(now.Add(5*time.Second), "a", "", time.Time{})
			mustAlertSnapshot(t, ctx, store, recovered)
			e, c, _ = store.OperatorAlertEpisode(ctx, id)
			if e.ResolvedAt == nil || !e.ResolvedAt.Equal(recovered.ObservedAt) || e.Status(c, recovered.ObservedAt) != "resolved" {
				t.Fatalf("recovery=%+v", e)
			}
			// Returning values must not alias mutable stored resolution pointers.
			*e.ResolvedAt = now.Add(-time.Hour)
			saved, _, _ := store.OperatorAlertEpisode(ctx, id)
			if !saved.ResolvedAt.Equal(recovered.ObservedAt) {
				t.Fatal("caller mutated retained resolution")
			}
			// Delayed polls and failed attempts cannot overwrite a newer snapshot.
			mustAlertSnapshot(t, ctx, store, first)
			if err := store.RecordOperatorAlertFailure(ctx, now); err != nil {
				t.Fatal(err)
			}
			staleEval := first
			staleEval.ObservedAt = now.Add(6 * time.Second)
			mustAlertSnapshot(t, ctx, store, staleEval)
			e, c, _ = store.OperatorAlertEpisode(ctx, id)
			if e.ResolvedAt == nil || !e.ResolvedAt.Equal(recovered.ObservedAt) {
				t.Fatalf("stale poll reopened episode: %+v", e)
			}
			// Re-firing has the same label identity and a distinct immutable episode.
			recurrence := alertSnapshotFixture(now.Add(7*time.Second), "a", "b", now.Add(7*time.Second))
			mustAlertSnapshot(t, ctx, store, recurrence)
			next := recurrence.Episodes[0]
			if next.ID == id {
				t.Fatal("recurrence reused episode ID")
			}
			latest, c, err := store.OperatorAlertEpisode(ctx, next.SeriesID)
			if err != nil || latest.ID != next.ID || latest.Status(c, recurrence.ObservedAt) != "firing" {
				t.Fatalf("legacy alias=%+v err=%v", latest, err)
			}
			old, _, _ := store.OperatorAlertEpisode(ctx, id)
			if old.ResolvedAt == nil {
				t.Fatal("old episode no longer inspectable")
			}
			filter.Now = recurrence.ObservedAt
			filter.Status = "resolved"
			page, err = store.OperatorAlertEpisodes(ctx, filter)
			if err != nil || len(page.Episodes) != 1 || page.Episodes[0].ID != id {
				t.Fatalf("resolved filter=%+v err=%v", page, err)
			}
			filter.Status = "firing"
			page, err = store.OperatorAlertEpisodes(ctx, filter)
			if err != nil || len(page.Episodes) != 1 || page.Episodes[0].ID != next.ID {
				t.Fatalf("firing filter=%+v err=%v", page, err)
			}
			filter.Now = now.Add(api.ObsHealthFreshnessMaxAge + 8*time.Second)
			filter.Status = "unknown"
			page, err = store.OperatorAlertEpisodes(ctx, filter)
			if err != nil || len(page.Episodes) != 2 {
				t.Fatalf("stale source filter=%+v err=%v", page, err)
			}
			// Reordered concurrent writers converge on the latest collection start.
			newer := alertSnapshotFixture(now.Add(10*time.Second), "a", "", time.Time{})
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, s := range []state.OperatorAlertSnapshot{recurrence, newer} {
				wg.Add(1)
				go func(s state.OperatorAlertSnapshot) {
					defer wg.Done()
					errs <- store.RecordOperatorAlertSnapshot(ctx, s)
				}(s)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			latest, c, err = store.OperatorAlertEpisode(ctx, next.ID)
			if err != nil || latest.ResolvedAt == nil || !latest.ResolvedAt.Equal(newer.ObservedAt) || !c.LastSuccessAt.Equal(newer.ObservedAt) {
				t.Fatalf("concurrent order: %+v %+v err=%v", latest, c, err)
			}
			// Pruning is strict at the retention boundary and capped per pass.
			if n, err := store.PruneOperatorAlertHistory(ctx, recovered.ObservedAt, 1); err != nil || n != 1 {
				t.Fatalf("prune older unknown: %d %v", n, err)
			}
			if _, _, err := store.OperatorAlertEpisode(ctx, id); err != nil {
				t.Fatalf("boundary episode pruned: %v", err)
			}
			if n, err := store.PruneOperatorAlertHistory(ctx, recovered.ObservedAt.Add(time.Microsecond), 1); err != nil || n != 1 {
				t.Fatalf("prune resolved: %d %v", n, err)
			}
			if _, _, err := store.OperatorAlertEpisode(ctx, id); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("expired episode retained: %v", err)
			}
			if _, _, err := store.OperatorAlertEpisode(ctx, next.ID); err != nil {
				t.Fatalf("recent recurrence pruned: %v", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := store.OperatorAlertEpisodes(canceled, filter); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read: %v", err)
			}
			if err := store.RecordOperatorAlertSnapshot(canceled, first); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled write: %v", err)
			}
		})
	}
}

func TestOperatorAlertHistoryPaginationAndValidation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			s := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
			for _, letter := range []string{"c", "d"} {
				other := alertSnapshotFixture(now, "a", letter, now.Add(-time.Minute))
				s.Episodes = append(s.Episodes, other.Episodes[0])
				s.Rules[0].EpisodeIDs = append(s.Rules[0].EpisodeIDs, other.Episodes[0].ID)
			}
			mustAlertSnapshot(t, ctx, store, s)
			f := state.OperatorAlertFilter{Now: now, Since: now.Add(-time.Hour), Limit: 2}
			page, err := store.OperatorAlertEpisodes(ctx, f)
			if err != nil || len(page.Episodes) != 2 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			f.BeforeAt = page.Episodes[1].ObservedAt()
			f.BeforeID = "platform_alert/" + page.Episodes[1].ID
			second, err := store.OperatorAlertEpisodes(ctx, f)
			if err != nil || len(second.Episodes) != 1 || second.Episodes[0].ID >= page.Episodes[1].ID {
				t.Fatalf("tie pagination=%+v err=%v", second, err)
			}
			f.Severity = "warning"
			empty, err := store.OperatorAlertEpisodes(ctx, f)
			if err != nil || len(empty.Episodes) != 0 || empty.Episodes == nil {
				t.Fatalf("severity=%+v err=%v", empty, err)
			}
			for _, mutate := range []func(*state.OperatorAlertSnapshot){
				func(s *state.OperatorAlertSnapshot) { s.Rules = nil }, func(s *state.OperatorAlertSnapshot) { s.Rules[0].EvaluatedAt = now.Add(-time.Hour) },
				func(s *state.OperatorAlertSnapshot) { s.Rules[0].EvaluatedAt = now.Add(time.Hour) }, func(s *state.OperatorAlertSnapshot) { s.Episodes[0].ID = strings.Repeat("f", 64) },
				func(s *state.OperatorAlertSnapshot) { s.Episodes[0].AccountID = "../../private" }, func(s *state.OperatorAlertSnapshot) { s.Episodes[0].Component = "private\nlabel" },
				func(s *state.OperatorAlertSnapshot) {
					s.Episodes[0].Name = strings.Repeat("a", api.ObsAlertNameMaxBytes+1)
				},
				func(s *state.OperatorAlertSnapshot) { s.Rules = append(s.Rules, s.Rules[0]) },
			} {
				bad := alertSnapshotFixture(now.Add(time.Second), "a", "b", now.Add(-time.Minute))
				mutate(&bad)
				if err := store.RecordOperatorAlertSnapshot(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("accepted invalid snapshot %+v: %v", bad, err)
				}
			}
			after, err := store.OperatorAlertCollectionStatus(ctx)
			if err != nil || !after.LastSuccessAt.Equal(now) {
				t.Fatalf("invalid writes changed state: %+v %v", after, err)
			}
			if _, err := store.PruneOperatorAlertHistory(ctx, now, api.ObsAlertHistoryPruneBatch+1); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("unbounded prune: %v", err)
			}
			f.Limit = api.ObsIncidentLimitMax + 2
			if _, err := store.OperatorAlertEpisodes(ctx, f); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("unbounded read: %v", err)
			}
		})
	}
}

func TestOperatorAlertHistoryDatabaseFailureAndRestart(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
	mustAlertSnapshot(t, ctx, store, s)
	next := state.NewPgStore(pool)
	e, _, err := next.OperatorAlertEpisode(ctx, s.Episodes[0].ID)
	if err != nil || e.ID != s.Episodes[0].ID {
		t.Fatalf("restart lost episode: %+v %v", e, err)
	}
	pool.Close()
	if err := next.RecordOperatorAlertSnapshot(ctx, s); err == nil {
		t.Fatal("closed database accepted snapshot")
	}
	if _, err := next.OperatorAlertCollectionStatus(ctx); err == nil {
		t.Fatal("closed database returned healthy collection")
	}
	if _, _, err := next.OperatorAlertEpisode(ctx, e.ID); err == nil {
		t.Fatal("closed database returned evidence")
	}
}

func TestOperatorAlertHistoryLegacyAliasSameActivation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			started := now.Add(-time.Minute)
			early := alertSnapshotFixture(now, "a", "b", started)
			late := alertSnapshotFixture(now.Add(time.Second), "c", "b", started)
			// Make lexical ID order disagree with observation order deterministically.
			if late.Episodes[0].ID > early.Episodes[0].ID {
				early, late = late, early
			}
			early.ObservedAt = now
			early.Rules[0].EvaluatedAt = now
			late.ObservedAt = now.Add(time.Second)
			late.Rules[0].EvaluatedAt = late.ObservedAt
			mustAlertSnapshot(t, ctx, store, early)
			mustAlertSnapshot(t, ctx, store, late)
			e, c, err := store.OperatorAlertEpisode(ctx, late.Episodes[0].SeriesID)
			if err != nil || e.ID != late.Episodes[0].ID || e.Status(c, late.ObservedAt) != "firing" {
				t.Fatalf("legacy link picked an older rule definition: %+v %v", e, err)
			}
		})
	}
}

func TestOperatorAlertHistoryTransactionRollback(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
	mustAlertSnapshot(t, ctx, store, first)
	// Force a failure after rule advancement and old-episode resolution.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_operator_alert_capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test capture failure'; END $$;
CREATE TRIGGER reject_operator_alert_capture BEFORE INSERT ON operator_alert_episodes FOR EACH ROW EXECUTE FUNCTION reject_operator_alert_capture()`); err != nil {
		t.Fatal(err)
	}
	next := alertSnapshotFixture(now.Add(time.Second), "a", "b", now.Add(time.Second))
	if err := store.RecordOperatorAlertSnapshot(ctx, next); err == nil {
		t.Fatal("injected failure returned success")
	}
	e, c, err := store.OperatorAlertEpisode(ctx, first.Episodes[0].ID)
	if err != nil || e.ResolvedAt != nil || !c.LastSuccessAt.Equal(now) || !c.LastAttemptAt.Equal(now) {
		t.Fatalf("partial transaction escaped: %+v %+v err=%v", e, c, err)
	}
	if _, _, err := store.OperatorAlertEpisode(ctx, next.Episodes[0].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed episode retained: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP TRIGGER reject_operator_alert_capture ON operator_alert_episodes"); err != nil {
		t.Fatal(err)
	}
	mustAlertSnapshot(t, ctx, store, next)
	e, c, err = store.OperatorAlertEpisode(ctx, next.Episodes[0].ID)
	if err != nil || e.Status(c, next.ObservedAt) != "firing" {
		t.Fatalf("retry after rollback failed: %+v err=%v", e, err)
	}
}

func TestOperatorAlertCollectionHealth(t *testing.T) {
	now := time.Now().UTC()
	c := state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}
	for _, tc := range []struct {
		delta         time.Duration
		state, reason string
	}{{0, "current", ""}, {api.ObsHealthFreshnessMaxAge, "current", ""}, {api.ObsHealthFreshnessMaxAge + time.Nanosecond, "unknown", "observation_stale"}, {-api.ObsHealthClockSkewTolerance - time.Nanosecond, "unknown", "observation_in_future"}} {
		t.Run(fmt.Sprint(tc.delta), func(t *testing.T) {
			h := c.Health(now.Add(tc.delta))
			if !reflect.DeepEqual([]string{h.State, h.Reason}, []string{tc.state, tc.reason}) {
				t.Fatalf("health=%+v", h)
			}
		})
	}
}

func mustAlertSnapshot(t *testing.T, ctx context.Context, store state.OperatorAlertHistoryStore, s state.OperatorAlertSnapshot) {
	t.Helper()
	if err := store.RecordOperatorAlertSnapshot(ctx, s); err != nil {
		t.Fatal(err)
	}
}
