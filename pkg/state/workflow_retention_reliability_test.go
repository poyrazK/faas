package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEventsOutlivePrunedRunUntilTheirOwnRetention(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		for _, event := range []*state.WorkflowEvent{
			{RunID: run.ID, EventName: "first", Payload: json.RawMessage(`{}`)},
			{RunID: run.ID, EventName: "second", Payload: json.RawMessage(`{}`)},
		} {
			if err := store.InsertWorkflowEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
			t.Fatalf("prune run: %d, %v", n, err)
		}
		events, err := store.GetWorkflowEventsForRun(ctx, run.ID)
		if err != nil || len(events) != 2 {
			t.Fatalf("events after run cleanup: %+v, %v", events, err)
		}
		if n, err := store.SweepExpiredWorkflowEvents(ctx, 30*24*time.Hour); err != nil || n != 0 {
			t.Fatalf("recent events pruned early: %d, %v", n, err)
		}
		if n, err := store.SweepExpiredWorkflowEvents(ctx, 0); err != nil || n != 2 {
			t.Fatalf("prune expired events: %d, %v", n, err)
		}
	})
}

func TestWorkflowEventArchiveIsBoundedAndPreservesReads(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	const extra = 7
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events (run_id,event_name,payload)
		SELECT $1, 'ready', jsonb_build_object('index', n) FROM generate_series(1,$2) n`, run.ID, api.WorkflowRetentionArchiveBatch+extra); err != nil {
		t.Fatal(err)
	}
	retention := state.WorkflowEventRetentionProgressStore(store)
	if n, more, err := retention.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || n != 0 || more {
		t.Fatalf("active run archival: deleted=%d more=%v err=%v", n, more, err)
	}
	var eventID string
	var payload json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT id::text,payload FROM workflow_events WHERE run_id=$1 LIMIT 1`, run.ID).Scan(&eventID, &payload); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := store.CompleteWorkflowCallback(ctx, run.ID, "wait", "ready", eventID, time.Hour, payload); err != nil || !duplicate {
		t.Fatalf("active callback retry: duplicate=%v err=%v", duplicate, err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
		t.Fatalf("prune run: %d, %v", n, err)
	}
	var live, archived int
	count := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_events WHERE run_id=$1),
			(SELECT count(*) FROM workflow_event_archive WHERE run_id=$1)`, run.ID).Scan(&live, &archived); err != nil {
			t.Fatal(err)
		}
	}
	count()
	if live != api.WorkflowRetentionArchiveBatch+extra || archived != 0 {
		t.Fatalf("run deletion moved events: live=%d archived=%d", live, archived)
	}
	if n, more, err := retention.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || n != 0 || !more {
		t.Fatalf("first archive batch: deleted=%d more=%v err=%v", n, more, err)
	}
	count()
	if live != extra || archived != api.WorkflowRetentionArchiveBatch {
		t.Fatalf("archive batch was not bounded: live=%d archived=%d", live, archived)
	}
	if events, err := store.GetWorkflowEventsForRun(ctx, run.ID); err != nil || len(events) != api.WorkflowRetentionArchiveBatch+extra {
		t.Fatalf("events during archive: %d, %v", len(events), err)
	}
	if _, err := store.FindMatchingEvent(ctx, run.ID, "ready"); err != nil {
		t.Fatalf("event lookup during archive: %v", err)
	}
	if n, more, err := retention.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || n != 0 || more {
		t.Fatalf("last archive batch: deleted=%d more=%v err=%v", n, more, err)
	}
	count()
	if live != 0 || archived != api.WorkflowRetentionArchiveBatch+extra {
		t.Fatalf("incomplete archive: live=%d archived=%d", live, archived)
	}
	var owners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_owners WHERE run_id=$1`, run.ID).Scan(&owners); err != nil || owners != 0 {
		t.Fatalf("completed owner: %d, %v", owners, err)
	}
	if n, err := store.SweepExpiredWorkflowEvents(ctx, 0); err != nil || n != api.WorkflowRetentionArchiveBatch+extra {
		t.Fatalf("expire archived events: %d, %v", n, err)
	}
}

func TestWorkflowEventArchiveCollisionRollsBack(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	event := &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}
	if err := store.InsertWorkflowEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_event_archive(id,run_id,app_id,event_name,payload,received_at)
		VALUES($1,$2,$3,'different','{}',now())`, event.ID, uuid.NewString(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("archive collision: %v", err)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_events WHERE id=$1`, event.ID).Scan(&live); err != nil || live != 1 {
		t.Fatalf("collision lost the live event: %d, %v", live, err)
	}
}

func TestWorkflowEventArchiveContinuesPastConflictingEventIDs(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	runs := []*state.WorkflowRun{
		{AppID: app.ID, WorkflowName: "collision", DefinitionSnapshot: json.RawMessage(`{"name":"collision","steps":[]}`)},
		{AppID: app.ID, WorkflowName: "later", DefinitionSnapshot: json.RawMessage(`{"name":"later","steps":[]}`)},
	}
	for _, run := range runs {
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events(id,run_id,event_name,payload)
		SELECT ('90000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,$1,'ready','{}'
		FROM generate_series(1,$2) n`, runs[0].ID, api.WorkflowRetentionArchiveBatch+1); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: runs[1].ID, EventName: "ready", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_event_archive(id,run_id,app_id,event_name,payload,received_at)
		SELECT id,$2,$3,'conflict','{}',now() FROM workflow_events WHERE run_id=$1 ORDER BY id LIMIT $4`, runs[0].ID, uuid.NewString(), app.ID, api.WorkflowRetentionArchiveBatch); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != len(runs) {
		t.Fatalf("prune runs: %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_event_owners SET deleted_at=now()-interval '1 hour' WHERE run_id=$1`, runs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, more, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); !errors.Is(err, state.ErrConflict) || !more {
		t.Fatalf("conflicting page: more=%v err=%v", more, err)
	}
	if _, more, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || more {
		t.Fatalf("following page: more=%v err=%v", more, err)
	}
	var liveFirst, archivedFirst, archivedLater int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_events WHERE run_id=$1),
		(SELECT count(*) FROM workflow_event_archive WHERE run_id=$1),
		(SELECT count(*) FROM workflow_event_archive WHERE run_id=$2)`, runs[0].ID, runs[1].ID).Scan(&liveFirst, &archivedFirst, &archivedLater); err != nil {
		t.Fatal(err)
	}
	if liveFirst != api.WorkflowRetentionArchiveBatch || archivedFirst != 1 || archivedLater != 1 {
		t.Fatalf("live=%d first archived=%d later archived=%d", liveFirst, archivedFirst, archivedLater)
	}
}

func TestWorkflowEventOwnershipAndAppDeletion(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events(run_id,event_name,payload) VALUES($1,'missing','{}')`, uuid.NewString()); err == nil {
		t.Fatal("event without a run was accepted")
	}
	event := &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}
	if err := store.InsertWorkflowEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	otherApp, _ := state.SeedWorkflowSchedule(t, store, "allow")
	for _, change := range []struct {
		query string
		value string
	}{
		{`UPDATE workflow_runs SET id=$2 WHERE id=$1`, uuid.NewString()},
		{`UPDATE workflow_runs SET app_id=$2 WHERE id=$1`, otherApp.ID},
	} {
		_, err := pool.Exec(ctx, change.query, run.ID, change.value)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("run ownership change with events: %v", err)
		}
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil {
		t.Fatal(err)
	}
	duplicate := &state.WorkflowRun{ID: run.ID, AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: run.DefinitionSnapshot}
	if err := store.CreateWorkflowRun(ctx, duplicate); err == nil {
		t.Fatal("run id with retained events was reused")
	}
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	var live, owners int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_events WHERE run_id=$1),
		(SELECT count(*) FROM workflow_event_owners WHERE run_id=$1)`, run.ID).Scan(&live, &owners); err != nil || live != 0 || owners != 0 {
		t.Fatalf("app deletion retained event data: live=%d owners=%d err=%v", live, owners, err)
	}
}

func TestWorkflowEventOwnerDoesNotWaitForLockedApp(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	event := &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}
	if err := store.InsertWorkflowEvent(deadline, event); err == nil || deadline.Err() != nil {
		t.Fatalf("event write should promptly retry when app is locked: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertWorkflowEvent(ctx, event); err != nil {
		t.Fatalf("event retry after unlocking app: %v", err)
	}
}

func TestWorkflowRetentionRemovesEmptyEventOwner(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	event := &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}
	if err := store.InsertWorkflowEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workflow_events WHERE id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
		t.Fatalf("prune eventless run: %d, %v", n, err)
	}
	var owners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_owners WHERE run_id=$1`, run.ID).Scan(&owners); err != nil || owners != 0 {
		t.Fatalf("eventless owner remains: %d, %v", owners, err)
	}
}

func TestWorkflowEventArchiveSkipsLockedApp(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	lockedApp, _ := state.SeedWorkflowSchedule(t, store, "allow")
	otherApp, _ := state.SeedWorkflowSchedule(t, store, "allow")
	runs := make([]*state.WorkflowRun, 0, 2)
	for _, app := range []state.App{lockedApp, otherApp} {
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 2 {
		t.Fatalf("prune runs: %d, %v", n, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, lockedApp.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, _, err := store.SweepExpiredWorkflowEventsWithProgress(deadline, 30*24*time.Hour); err != nil {
		t.Fatalf("archive with locked app: %v", err)
	}
	var lockedLive, otherArchived int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_events WHERE run_id=$1),
		(SELECT count(*) FROM workflow_event_archive WHERE run_id=$2)`, runs[0].ID, runs[1].ID).Scan(&lockedLive, &otherArchived); err != nil || lockedLive != 1 || otherArchived != 1 {
		t.Fatalf("locked live=%d other archived=%d err=%v", lockedLive, otherArchived, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil {
		t.Fatalf("archive after unlock: %v", err)
	}
}

func TestWorkflowEventArchiveDoesNotSpinOnLockedEvents(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs(app_id,workflow_name,status,definition_snapshot,finished_at)
		SELECT $1,'locked','succeeded','{}',now() FROM generate_series(1,$2)`, app.ID, api.WorkflowRetentionArchiveBatch); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events(run_id,event_name,payload)
		SELECT id,'ready','{}' FROM workflow_runs WHERE app_id=$1 AND workflow_name='locked'`, app.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != api.WorkflowRetentionArchiveBatch {
		t.Fatalf("prune runs: %d, %v", n, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM workflow_events WHERE run_id IN
		(SELECT run_id FROM workflow_event_owners WHERE app_id=$1) FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if n, more, err := store.SweepExpiredWorkflowEventsWithProgress(deadline, 30*24*time.Hour); err != nil || n != 0 || !more {
		t.Fatalf("locked archive: deleted=%d more=%v err=%v", n, more, err)
	}
	if n, more, err := store.SweepExpiredWorkflowEventsWithProgress(deadline, 30*24*time.Hour); err != nil || n != 0 || more {
		t.Fatalf("end of locked archive scan: deleted=%d more=%v err=%v", n, more, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, more, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || n != 0 || !more {
		t.Fatalf("unlocked archive: deleted=%d more=%v err=%v", n, more, err)
	}
}

func TestWorkflowEventArchiveScansPastLockedOwners(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs(app_id,workflow_name,status,definition_snapshot,finished_at)
		SELECT $1,'fair-archive','succeeded','{}',now() FROM generate_series(1,$2)`, app.ID, api.WorkflowRetentionArchiveBatch+1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events(run_id,event_name,payload)
		SELECT id,'ready','{}' FROM workflow_runs WHERE app_id=$1 AND workflow_name='fair-archive'`, app.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != api.WorkflowRetentionArchiveBatch+1 {
		t.Fatalf("prune runs: %d, %v", n, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT e.id FROM workflow_events e JOIN
		(SELECT run_id FROM workflow_event_owners WHERE app_id=$1 ORDER BY deleted_at,run_id LIMIT $2) o ON o.run_id=e.run_id FOR UPDATE OF e`, app.ID, api.WorkflowRetentionArchiveBatch); err != nil {
		t.Fatal(err)
	}
	if _, more, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || !more {
		t.Fatalf("first page: more=%v err=%v", more, err)
	}
	if _, _, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var archived int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_archive WHERE app_id=$1`, app.ID).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("later owner archived=%d err=%v", archived, err)
	}
}

func TestWorkflowEventOwnerCleanupIsLimitedToSelectedPage(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs(app_id,workflow_name,status,definition_snapshot,finished_at)
		SELECT $1,'bounded-owner','succeeded','{}',now() FROM generate_series(1,$2)`, app.ID, api.WorkflowRetentionArchiveBatch+1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events(run_id,event_name,payload)
		SELECT id,'ready','{}' FROM workflow_runs WHERE app_id=$1 AND workflow_name='bounded-owner'`, app.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != api.WorkflowRetentionArchiveBatch+1 {
		t.Fatalf("prune runs: %d %v", n, err)
	}
	var last string
	if err := pool.QueryRow(ctx, `SELECT run_id::text FROM workflow_event_owners WHERE app_id=$1 ORDER BY deleted_at,run_id OFFSET $2 LIMIT 1`, app.ID, api.WorkflowRetentionArchiveBatch).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workflow_events WHERE run_id=$1`, last); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT e.id FROM workflow_events e JOIN
		(SELECT run_id FROM workflow_event_owners WHERE app_id=$1 ORDER BY deleted_at,run_id LIMIT $2) o ON o.run_id=e.run_id FOR UPDATE OF e`, app.ID, api.WorkflowRetentionArchiveBatch); err != nil {
		t.Fatal(err)
	}
	if _, more, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || !more {
		t.Fatalf("first page: more=%v err=%v", more, err)
	}
	var owners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_owners WHERE run_id=$1`, last).Scan(&owners); err != nil || owners != 1 {
		t.Fatalf("later owner pruned early: %d %v", owners, err)
	}
	if _, more, err := store.SweepExpiredWorkflowEventsWithProgress(ctx, 30*24*time.Hour); err != nil || more {
		t.Fatalf("second page: more=%v err=%v", more, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_owners WHERE run_id=$1`, last).Scan(&owners); err != nil || owners != 0 {
		t.Fatalf("empty owner retained: %d %v", owners, err)
	}
}

func TestWorkflowRunIDReuseWaitsForRetention(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM workflow_runs WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	pid := conn.Conn().PgConn().PID()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := conn.Exec(callCtx, `INSERT INTO workflow_runs(id,app_id,workflow_name,definition_snapshot) VALUES($1,$2,'retention','{}')`, run.ID, app.ID)
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT coalesce(wait_event='advisory', false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run id insertion did not wait for retention")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("reused run id: %v", err)
		}
	case <-callCtx.Done():
		t.Fatal("run id insertion did not finish after retention")
	}
	if events, err := store.GetWorkflowEventsForRun(ctx, run.ID); err != nil || len(events) != 1 {
		t.Fatalf("retained events after concurrent run creation: %+v, %v", events, err)
	}
}

func TestWorkflowEventInsertSerializesRunOwnershipChanges(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	other, _ := state.SeedWorkflowSchedule(t, store, "allow")
	for _, change := range []struct {
		name, query, value string
	}{
		{"run ID", `UPDATE workflow_runs SET id=$2 WHERE id=$1`, uuid.NewString()},
		{"app", `UPDATE workflow_runs SET app_id=$2 WHERE id=$1`, other.ID},
	} {
		t.Run(change.name, func(t *testing.T) {
			run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `INSERT INTO workflow_events(run_id,event_name,payload) VALUES($1,'ready','{}')`, run.ID); err != nil {
				t.Fatal(err)
			}
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			pid := conn.Conn().PgConn().PID()
			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := conn.Exec(callCtx, change.query, run.ID, change.value)
				result <- err
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("ownership change did not wait for event insertion")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
					t.Fatalf("ownership change after event insertion: %v", err)
				}
			case <-callCtx.Done():
				t.Fatal("ownership change did not finish after event insertion")
			}
			if events, err := store.GetWorkflowEventsForRun(ctx, run.ID); err != nil || len(events) != 1 {
				t.Fatalf("event after concurrent ownership change: %+v, %v", events, err)
			}
		})
	}
}

func TestWorkflowRunOwnershipChangeDoesNotWaitForIDLock(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	other, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('workflow-event-owner/' || $1::text, 0))`, run.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = pool.Exec(deadline, `UPDATE workflow_runs SET app_id=$2 WHERE id=$1`, run.ID, other.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" || deadline.Err() != nil {
		t.Fatalf("busy run ID should promptly request retry: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE workflow_runs SET app_id=$2 WHERE id=$1`, run.ID, other.ID)
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("existing app-ownership guard after ID unlock: %v", err)
	}
}

func TestWorkflowRetentionSkipsLockedAppsAndPrunesOthers(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	lockedApp, _ := state.SeedWorkflowSchedule(t, store, "allow")
	otherApp, _ := state.SeedWorkflowSchedule(t, store, "allow")
	runs := make([]*state.WorkflowRun, 0, 2)
	for _, app := range []state.App{lockedApp, otherApp} {
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	// Fill an entire candidate page with older runs from the locked app. A
	// trigger that skips these after selection would otherwise hide the other
	// app's run from this pass.
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs (app_id, workflow_name, status, definition_snapshot, finished_at)
		SELECT $1, 'retention', 'succeeded', '{"name":"retention","steps":[]}'::jsonb, now() - interval '1 hour'
		FROM generate_series(1, $2)`, lockedApp.ID, api.WorkflowRetentionPruneBatch); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id = $1 FOR UPDATE`, lockedApp.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if n, err := store.SweepExpiredWorkflowRuns(deadline, 0); err != nil || n != 1 {
		t.Fatalf("prune unlocked app while another is locked: %d, %v", n, err)
	}
	if _, err := store.GetWorkflowRun(ctx, runs[0].ID); err != nil {
		t.Fatalf("locked app's run disappeared: %v", err)
	}
	if events, err := store.GetWorkflowEventsForRun(ctx, runs[1].ID); err != nil || len(events) != 1 {
		t.Fatalf("unlocked app's archived events: %+v, %v", events, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != api.WorkflowRetentionPruneBatch {
		t.Fatalf("prune formerly locked app's first page: %d, %v", n, err)
	}
	if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
		t.Fatalf("prune formerly locked app's remaining run: %d, %v", n, err)
	}
}
