package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261015173127929: receipts are ordered by revision and isolated by episode.
func seedTriageHistory(t *testing.T, ctx context.Context, store state.Store, now time.Time) string {
	t.Helper()
	fixture := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
	mustAlertSnapshot(t, ctx, store, fixture)
	id := fixture.Episodes[0].ID
	trace := strings.Repeat("e", 32)
	until := now.Add(time.Hour)
	for i, action := range []string{"acknowledge", "claim", "snooze", "unsnooze"} {
		at := now.Add(time.Duration(3-i) * time.Microsecond) // Receipt clocks deliberately run backwards.
		req := state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: action, ExpectedRevision: int64(i), Now: at, TraceID: &trace}
		if action == "snooze" {
			req.SnoozedUntil = &until
		}
		if _, err := store.MutateOperatorAlertTriage(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestOperatorAlertTriageHistoryStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
			first := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
			mustAlertSnapshot(t, ctx, store, first)
			id := first.Episodes[0].ID
			empty, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 2})
			if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.Coverage.State != "complete" || empty.Coverage.AvailableEntries != 0 || empty.Coverage.OldestAvailableRevision != nil {
				t.Fatalf("untouched: %+v %v", empty, err)
			}
			id = seedTriageHistory(t, ctx, store, now)
			f := state.OperatorAlertTriageHistoryFilter{ID: first.Episodes[0].SeriesID, Limit: 2}
			page, err := store.OperatorAlertTriageHistory(ctx, f)
			if err != nil || page.Episode.ID != id || page.SnapshotRevision != 4 || !page.Truncated || len(page.Items) != 2 || page.Items[0].Revision != 4 || page.Items[1].Revision != 3 || page.Items[0].At.After(page.Items[1].At) || page.Coverage.State != "complete" || page.Coverage.AvailableEntries != 4 || page.Coverage.OldestAvailableRevision == nil || *page.Coverage.OldestAvailableRevision != 1 {
				t.Fatalf("first page: %+v %v", page, err)
			}
			body, err := json.Marshal(page.Items)
			if err != nil || strings.Contains(string(body), triageActor) || strings.Contains(string(body), "owner_account_id") || !strings.Contains(string(body), strings.Repeat("e", 32)) {
				t.Fatalf("unsafe history: %s %v", body, err)
			}
			if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: "acknowledge", ExpectedRevision: 4, Now: now}); err != nil {
				t.Fatal(err)
			}
			upper := int64(4)
			f.SnapshotRevision = &upper
			f.ExpectedEpisodeID = id
			f.BeforeRevision = 3
			next, err := store.OperatorAlertTriageHistory(ctx, f)
			if err != nil || next.SnapshotRevision != 4 || next.Episode.Triage.Revision != 5 || next.Truncated || len(next.Items) != 2 || next.Items[0].Revision != 2 || next.Items[1].Revision != 1 || next.Coverage.AvailableEntries != 4 {
				t.Fatalf("pinned page: %+v %v", next, err)
			}
			// Current workflow state and receipt pointers returned to a caller do not alias storage.
			*page.Items[1].SnoozedUntil = now.Add(-time.Hour)
			again, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 5})
			if err != nil || again.Items[2].SnoozedUntil == nil || !again.Items[2].SnoozedUntil.After(now) {
				t.Fatalf("aliased history: %+v %v", again, err)
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now.Add(time.Second), "a", "", time.Time{}))
			recurrence := alertSnapshotFixture(now.Add(2*time.Second), "a", "b", now.Add(2*time.Second))
			mustAlertSnapshot(t, ctx, store, recurrence)
			if _, err := store.OperatorAlertTriageHistory(ctx, f); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("alias crossed recurrence: %v", err)
			}
			fresh, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: recurrence.Episodes[0].ID, Limit: 2})
			if err != nil || fresh.SnapshotRevision != 0 || len(fresh.Items) != 0 || fresh.Coverage.State != "complete" {
				t.Fatalf("recurrence inherited receipts: %+v %v", fresh, err)
			}
			for _, bad := range []state.OperatorAlertTriageHistoryFilter{{ID: id, Limit: 0}, {ID: id, Limit: 101}, {ID: "bad", Limit: 1}, {ID: id, Limit: 1, BeforeRevision: 1}, {ID: id, Limit: 1, SnapshotRevision: &upper, BeforeRevision: 5}} {
				if _, err := store.OperatorAlertTriageHistory(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("invalid query %+v: %v", bad, err)
				}
			}
		})
	}
}

func TestOperatorAlertTriageHistoryRejectsDuplicateReceipts(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			id := seedTriageHistory(t, ctx, store, now)
			logs, err := store.ListAuditLog(ctx, state.AuditLogFilter{KindPrefix: "operator.action.alert_triage", Limit: 10, IncludeAnonymous: true})
			if err != nil {
				t.Fatal(err)
			}
			duplicate := logs[0]
			duplicate.ID = uuid.New()
			if err := store.InsertAuditLog(ctx, duplicate); err != nil {
				t.Fatal(err)
			}
			if _, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 2}); err == nil {
				t.Fatal("duplicate revisions were reported as complete")
			}
		})
	}
}

func TestOperatorAlertTriageHistoryPostgresGapsAndRestart(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := seedTriageHistory(t, ctx, store, now)
	if _, err := pool.Exec(ctx, "DELETE FROM audit_log WHERE kind='operator.action.alert_triage' AND data->>'episode_id'=$1 AND data#>>'{triage,revision}'='2'", id); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	// Corrupt a selected payload without corrupting its revision index metadata.
	if _, err := pool.Exec(ctx, "UPDATE audit_log SET data=jsonb_set(data,'{triage,acknowledged}','null'::jsonb) WHERE kind='operator.action.alert_triage' AND data->>'episode_id'=$1 AND data#>>'{triage,revision}'='4'", id); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 4}); err == nil {
		t.Fatal("malformed receipt was exported")
	}
	if _, err := pool.Exec(ctx, "UPDATE audit_log SET data=jsonb_set(data,'{triage,acknowledged}','true'::jsonb) || jsonb_build_object('actor_email','private-email','trace_id','private-trace') WHERE kind='operator.action.alert_triage' AND data->>'episode_id'=$1 AND data#>>'{triage,revision}'='4'", id); err != nil {
		t.Fatal(err)
	}
	page, err := restarted.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 4})
	if err != nil || page.Coverage.State != "gaps" || page.Coverage.Reason != "missing_receipts" || page.Coverage.MissingEntries != 1 || page.Coverage.AvailableEntries != 3 || len(page.Items) != 3 || page.Items[0].TraceID != "" || page.Items[2].Revision != 1 {
		t.Fatalf("missing middle receipt: %+v %v", page, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM audit_log WHERE kind='operator.action.alert_triage' AND data->>'episode_id'=$1", id); err != nil {
		t.Fatal(err)
	}
	page, err = restarted.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 4})
	if err != nil || page.Coverage.State != "gaps" || page.Coverage.MissingEntries != 4 || page.Coverage.OldestAvailableRevision != nil || len(page.Items) != 0 {
		t.Fatalf("all receipts missing: %+v %v", page, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_log RENAME TO unavailable_audit_log"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 4}); err == nil {
		t.Fatal("missing audit storage became empty history")
	}
}

func TestOperatorAlertTriageHistoryConcurrentSnapshots(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			fixture := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
			mustAlertSnapshot(t, ctx, store, fixture)
			id := fixture.Episodes[0].ID
			done := make(chan error, 1)
			go func() {
				for i := range 12 {
					_, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: "acknowledge", ExpectedRevision: int64(i), Now: now})
					if err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			// Always join the writer before fixture cleanup, including failed assertions.
			t.Cleanup(func() {
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			for range 12 {
				page, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: 5})
				if err != nil || page.SnapshotRevision != page.Episode.Triage.Revision || page.Coverage.AvailableEntries != page.SnapshotRevision || page.Coverage.MissingEntries != 0 {
					t.Fatalf("torn triage snapshot: %+v %v", page, err)
				}
			}
		})
	}
}
