package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCacheScheduleActivityRetentionMemory(t *testing.T) {
	testActivityRetention(t, state.NewMemStore())
}
func TestCacheScheduleActivityRetentionPostgres(t *testing.T) {
	testActivityRetention(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testActivityRetention(t *testing.T, s state.Store) {
	f := newActivityFixture(t, s)
	ctx := t.Context()
	reader := s.(state.EnvironmentCacheScheduleActivityReader)
	pruner := s.(state.EnvironmentCacheScheduleActivityPruner)
	schedules := s.(state.EnvironmentCacheScheduleStore)
	initial := f.events(t, "", 1).Cursor
	// More than one cleanup batch, without breaching the active schedule quota.
	count := api.EnvironmentCacheActivityRetentionBatch + 4
	for i := 0; i < count/2; i++ {
		v := f.create(t, f.proposal(time.Now().Add(time.Hour)))
		if _, err := schedules.CancelEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", v.ScheduleID); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().Add(time.Minute)
	if err := pruner.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	_, err := reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", initial, 1)
	var expired *state.EnvironmentCacheActivityExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("expected expiry, got %v", err)
	}
	recovery := expired.Recovery
	floor, e := api.ParseEnvironmentCacheActivityCursor(recovery.OldestCursor, f.Account.ID, f.App.ID, f.Project.ID, f.Env.ID)
	if e != nil || floor.Sequence != api.EnvironmentCacheActivityRetentionBatch {
		t.Fatal("cleanup was not bounded", floor, e)
	}
	head, e := api.ParseEnvironmentCacheActivityCursor(recovery.RestartCursor, f.Account.ID, f.App.ID, f.Project.ID, f.Env.ID)
	if e != nil || head.Sequence != int64(count) || recovery.RetentionSeconds != api.EnvironmentCacheActivityRetentionSeconds {
		t.Fatal(recovery, e)
	}
	page := f.events(t, recovery.OldestCursor, 50)
	if len(page.Entries) != 4 || page.Entries[0].Sequence != floor.Sequence+1 {
		t.Fatal("valid consumer skipped events", page)
	}
	// The recovery head is captured before baseline discovery. A change during
	// discovery is replayed even if already represented in that live baseline.
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	replay := f.events(t, recovery.RestartCursor, 50)
	if len(replay.Entries) != 1 || replay.Entries[0].Sequence != head.Sequence+1 {
		t.Fatal("recovery lost new event", replay)
	}
	if err = pruner.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	_, err = reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", recovery.OldestCursor, 1)
	if !errors.As(err, &expired) {
		t.Fatalf("active consumer falling behind must expire: %v", err)
	}
	tail := f.events(t, expired.Recovery.RestartCursor, 1)
	if len(tail.Entries) != 0 {
		t.Fatal(tail)
	}
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	tail = f.events(t, tail.Cursor, 1)
	if len(tail.Entries) != 1 || tail.Entries[0].Sequence != int64(count+2) {
		t.Fatal("sequence reset after complete pruning", tail)
	}
	// Wrong-scope cursors remain invalid, not recoverable expiry disclosures.
	_, err = reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "production", initial, 1)
	if errors.As(err, &expired) {
		t.Fatal("foreign scope leaked expiry metadata")
	}
}

func TestCacheScheduleActivityRetentionSnapshotAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	f := newActivityFixture(t, s)
	ctx := t.Context()
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	q := sqlc.New()
	var appID, envID, accountID pgtype.UUID
	_ = appID.Scan(f.App.ID)
	_ = envID.Scan(f.Env.ID)
	_ = accountID.Scan(f.Account.ID)
	// A repeatable-read consumer established before pruning retains the old page.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	bounds, err := q.ReadEnvironmentCacheScheduleEventBounds(ctx, tx, sqlc.ReadEnvironmentCacheScheduleEventBoundsParams{AccountID: accountID, AppID: appID, EnvironmentID: envID})
	if err != nil || bounds.Head != 1 {
		t.Fatal(bounds, err)
	}
	cutoff := time.Now().Add(time.Minute)
	if err = s.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ReadEnvironmentCacheScheduleEvents(ctx, tx, sqlc.ReadEnvironmentCacheScheduleEventsParams{AccountID: accountID, AppID: appID, EnvironmentID: envID, PageLimit: 2})
	if err != nil || len(rows) != 1 {
		t.Fatal("concurrent cleanup broke old snapshot", rows, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", "", 1)
	var expired *state.EnvironmentCacheActivityExpiredError
	if !errors.As(err, &expired) {
		t.Fatal(err)
	}
	checkpoint := expired.Recovery.RestartCursor
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	// Force the floor update to fail after the DELETE CTE, proving atomic rollback.
	if _, err = pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_event_counters ADD CONSTRAINT test_no_prune CHECK (pruned_through<=1)"); err != nil {
		t.Fatal(err)
	}
	if err = s.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err == nil {
		t.Fatal("expected floor failure")
	}
	page := f.events(t, checkpoint, 1)
	if len(page.Entries) != 1 || page.Entries[0].Sequence != 2 {
		t.Fatal("failed pruning deleted events", page)
	}
	if _, err = pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_event_counters DROP CONSTRAINT test_no_prune"); err != nil {
		t.Fatal(err)
	}
	// A locked writer target is skipped rather than blocking the maintenance pass.
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, "SELECT 1 FROM environment_cache_schedule_event_counters WHERE app_id=$1 FOR UPDATE", f.App.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if len(f.events(t, checkpoint, 1).Entries) != 1 {
		t.Fatal("locked target pruned")
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", checkpoint, 1)
	if !errors.As(err, &expired) {
		t.Fatal("cleanup did not resume", err)
	}
}

func TestCacheScheduleActivityRetentionPostgresClockSkew(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	f := newActivityFixture(t, s)
	for i := 0; i < 3; i++ {
		f.create(t, f.proposal(time.Now().Add(time.Hour)))
	}
	if _, err := pool.Exec(t.Context(), "UPDATE environment_cache_schedule_events SET occurred_at=CASE WHEN sequence=2 THEN now() ELSE now()-interval '31 days' END WHERE app_id=$1", f.App.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneEnvironmentCacheScheduleActivity(t.Context(), time.Now().Add(-time.Duration(api.EnvironmentCacheActivityRetentionSeconds)*time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetEnvironmentCacheScheduleActivity(t.Context(), f.Account.ID, f.App.ID, "staging", "", 1)
	var expired *state.EnvironmentCacheActivityExpiredError
	if !errors.As(err, &expired) {
		t.Fatal(err)
	}
	page := f.events(t, expired.Recovery.OldestCursor, 50)
	if len(page.Entries) != 2 || page.Entries[0].Sequence != 2 || page.Entries[1].Sequence != 3 {
		t.Fatal("cleanup jumped past recent event", page)
	}
}
