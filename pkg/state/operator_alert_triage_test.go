package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const triageActor = "11111111-1111-4111-8111-111111111111"
const triageOther = "22222222-2222-4222-8222-222222222222"

func TestOperatorAlertTriageStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			first := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
			mustAlertSnapshot(t, ctx, store, first)
			id := first.Episodes[0].ID
			until := now.Add(time.Hour)
			trace := strings.Repeat("c", 32)
			req := state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Action: "acknowledge", Now: now, TraceID: &trace}
			for revision, action := range []string{"acknowledge", "claim", "snooze"} {
				req.Action = action
				req.ExpectedRevision = int64(revision)
				if action == "snooze" {
					req.SnoozedUntil = &until
				}
				got, err := store.MutateOperatorAlertTriage(ctx, req)
				if err != nil || got.Revision != int64(revision+1) {
					t.Fatalf("%s: %+v %v", action, got, err)
				}
			}
			episode, collection, err := store.OperatorAlertEpisode(ctx, id)
			if err != nil || episode.Status(collection, now) != "firing" || !episode.Triage.Acknowledged || episode.Triage.OwnerID != triageActor || !episode.Triage.Projection(now).Snoozed {
				t.Fatalf("independent state: %+v %v", episode, err)
			}
			req.SnoozedUntil = nil
			req.Action = "unacknowledge"
			req.ExpectedRevision = 0
			if _, err := store.MutateOperatorAlertTriage(ctx, req); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale revision: %v", err)
			}
			req.ExpectedRevision = 3
			req.ActorID = triageOther
			req.Action = "release"
			if _, err := store.MutateOperatorAlertTriage(ctx, req); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign release: %v", err)
			}
			req.Action = "claim"
			if _, err := store.MutateOperatorAlertTriage(ctx, req); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("takeover: %v", err)
			}
			req.EpisodeID = first.Episodes[0].SeriesID
			if _, err := store.MutateOperatorAlertTriage(ctx, req); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("alias write: %v", err)
			}
			// Mutating returned timestamp pointers cannot change stored snoozes.
			*episode.Triage.SnoozedUntil = now.Add(-time.Hour)
			episode, _, _ = store.OperatorAlertEpisode(ctx, id)
			if !episode.Triage.Projection(now).Snoozed || episode.Triage.Projection(until).Snoozed {
				t.Fatal("snooze aliasing or expiry")
			}
			mustAlertSnapshot(t, ctx, store, alertSnapshotFixture(now.Add(time.Second), "a", "", time.Time{}))
			recurrence := alertSnapshotFixture(now.Add(2*time.Second), "a", "b", now.Add(2*time.Second))
			mustAlertSnapshot(t, ctx, store, recurrence)
			fresh, _, err := store.OperatorAlertEpisode(ctx, recurrence.Episodes[0].ID)
			if err != nil || fresh.Triage.Revision != 0 || fresh.Triage.Acknowledged || fresh.Triage.OwnerID != "" || fresh.Triage.SnoozedUntil != nil {
				t.Fatalf("recurrence inherited triage: %+v %v", fresh, err)
			}
			// The matching old episode sorts after the new episode: limit=1 must still find it.
			base := state.OperatorAlertFilter{Now: now.Add(2 * time.Second), Since: now.Add(-time.Hour), Limit: 1}
			for _, field := range []string{"acknowledgment", "ownership", "mine", "snooze"} {
				f := base
				switch field {
				case "acknowledgment":
					f.Acknowledgment = "acknowledged"
				case "ownership":
					f.Ownership = "owned"
				case "mine":
					f.Ownership = "mine"
					f.OwnerID = triageActor
				case "snooze":
					f.Snooze = "active"
				}
				page, err := store.OperatorAlertEpisodes(ctx, f)
				if err != nil || len(page.Episodes) != 1 || page.Episodes[0].ID != id {
					t.Fatalf("pre-limit %s: %+v %v", field, page, err)
				}
			}
			base.Now = until
			base.Snooze = "inactive"
			base.Acknowledgment = "acknowledged"
			page, err := store.OperatorAlertEpisodes(ctx, base)
			if err != nil || len(page.Episodes) != 1 || page.Episodes[0].ID != id {
				t.Fatalf("expired snooze: %+v %v", page, err)
			}
			logs, err := store.ListAuditLog(ctx, state.AuditLogFilter{KindPrefix: "operator.action.alert_triage", Limit: 100, IncludeAnonymous: true})
			if err != nil || len(logs) != 3 || logs[0].Actor != triageActor || !strings.Contains(string(logs[0].Data), trace) {
				t.Fatalf("audit: %+v %v", logs, err)
			}
			// All remaining inverse actions persist and advance revisions.
			req = state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: triageActor, Now: now, ExpectedRevision: 3}
			for _, action := range []string{"unacknowledge", "release", "unsnooze"} {
				req.Action = action
				if _, err := store.MutateOperatorAlertTriage(ctx, req); err != nil {
					t.Fatal(err)
				}
				req.ExpectedRevision++
			}
			old, _, _ := store.OperatorAlertEpisode(ctx, id)
			if old.Triage.Acknowledged || old.Triage.OwnerID != "" || old.Triage.SnoozedUntil != nil || old.Triage.Revision != 6 {
				t.Fatalf("inverse: %+v", old.Triage)
			}
			// Episode retention also removes its triage, while the FK-free audit survives.
			if _, err := store.PruneOperatorAlertHistory(ctx, now.Add(3*time.Second), api.ObsAlertHistoryPruneBatch); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.OperatorAlertEpisode(ctx, id); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("pruned triage episode: %v", err)
			}
			logs, err = store.ListAuditLog(ctx, state.AuditLogFilter{KindPrefix: "operator.action.alert_triage", Limit: 100, IncludeAnonymous: true})
			if err != nil || len(logs) != 6 {
				t.Fatalf("audit removed with episode: count=%d err=%v", len(logs), err)
			}
		})
	}
}

func TestOperatorAlertTriageConcurrentClaims(t *testing.T) {
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
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, actor := range []string{triageActor, triageOther} {
				wg.Go(func() {
					_, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: fixture.Episodes[0].ID, ActorID: actor, Action: "claim", Now: now})
					results <- err
				})
			}
			wg.Wait()
			close(results)
			success, conflict := 0, 0
			for err := range results {
				if err == nil {
					success++
				} else if errors.Is(err, state.ErrConflict) {
					conflict++
				} else {
					t.Fatal(err)
				}
			}
			if success != 1 || conflict != 1 {
				t.Fatalf("success=%d conflict=%d", success, conflict)
			}
		})
	}
}

func TestOperatorAlertTriageRejectsInvalidInput(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now().UTC()
	until := now.Add(api.ObsAlertTriageSnoozeMax + time.Second)
	base := state.OperatorAlertTriageMutation{EpisodeID: strings.Repeat("a", 64), ActorID: triageActor, Action: "acknowledge", Now: now}
	for _, change := range []func(*state.OperatorAlertTriageMutation){
		func(m *state.OperatorAlertTriageMutation) { m.ExpectedRevision = -1 },
		func(m *state.OperatorAlertTriageMutation) { m.ActorID = "invalid" },
		func(m *state.OperatorAlertTriageMutation) { m.Action = "resolve" },
		func(m *state.OperatorAlertTriageMutation) { m.Action = "snooze" },
		func(m *state.OperatorAlertTriageMutation) { m.Action = "snooze"; m.SnoozedUntil = &until },
		func(m *state.OperatorAlertTriageMutation) { m.SnoozedUntil = &until },
	} {
		m := base
		change(&m)
		if _, err := store.MutateOperatorAlertTriage(context.Background(), m); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid request %+v: %v", m, err)
		}
	}
}

func TestOperatorAlertTriagePostgresRestartAndAuditRollback(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture := alertSnapshotFixture(now, "a", "b", now.Add(-time.Minute))
	mustAlertSnapshot(t, ctx, store, fixture)
	req := state.OperatorAlertTriageMutation{EpisodeID: fixture.Episodes[0].ID, ActorID: triageActor, Action: "acknowledge", Now: now}
	if _, err := store.MutateOperatorAlertTriage(ctx, req); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	e, _, err := restarted.OperatorAlertEpisode(ctx, req.EpisodeID)
	if err != nil || !e.Triage.Acknowledged || e.Triage.Revision != 1 {
		t.Fatalf("restart: %+v %v", e, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_log ADD CONSTRAINT reject_triage_audit CHECK (kind <> 'operator.action.alert_triage') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = 1
	req.Action = "unacknowledge"
	if _, err := restarted.MutateOperatorAlertTriage(ctx, req); err == nil {
		t.Fatal("audit failure accepted")
	}
	e, _, err = restarted.OperatorAlertEpisode(ctx, req.EpisodeID)
	if err != nil || !e.Triage.Acknowledged || e.Triage.Revision != 1 {
		t.Fatalf("audit failure leaked write: %+v %v", e, err)
	}
}
