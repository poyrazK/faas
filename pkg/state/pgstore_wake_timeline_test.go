package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func insertWakeTimelineEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actor, kind string, data map[string]any, at time.Time) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal %s: %v", kind, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO events (actor, kind, data, at) VALUES ($1, $2, $3, $4)`,
		actor, kind, raw, at); err != nil {
		t.Fatalf("insert %s: %v", kind, err)
	}
}

func TestPgStore_ListEventsByWakeID_DeduplicatesMirrorBeforePaging(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)
	wakeID := "wake-sql-dedup-" + uuid.NewString()
	base := time.Now().UTC().Add(-time.Minute)
	canonical := map[string]any{
		"wake_id": wakeID, "app_id": uuid.NewString(), "trigger": "gateway", "at_capacity": true,
	}
	mirror := map[string]any{
		"wake_id": wakeID, "app_id": canonical["app_id"], "trigger": "gateway", "at_capacity": false,
	}
	insertWakeTimelineEvent(t, ctx, pool, "vmmd", "wake.boot_started", mirror, base)
	insertWakeTimelineEvent(t, ctx, pool, "schedd", "wake.boot_started", canonical, base.Add(100*time.Millisecond))
	insertWakeTimelineEvent(t, ctx, pool, "schedd", "wake.boot_completed", map[string]any{"wake_id": wakeID}, base.Add(200*time.Millisecond))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM events WHERE data->>'wake_id' = $1`, wakeID)
	})

	rows, err := s.ListEventsByWakeID(ctx, wakeID, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want canonical boot + completion", len(rows))
	}
	if rows[0].Actor != "schedd" || rows[0].Kind != "wake.boot_started" {
		t.Fatalf("first row = %s/%s, want schedd/wake.boot_started", rows[0].Actor, rows[0].Kind)
	}
	if rows[1].Kind != "wake.boot_completed" {
		t.Fatalf("second row kind = %q, want wake.boot_completed", rows[1].Kind)
	}

	// Applying a cursor after the canonical row must not expose the
	// earlier mirror as a replacement boot marker.
	rows, err = s.ListEventsByWakeID(ctx, wakeID, base.Add(150*time.Millisecond), 100)
	if err != nil {
		t.Fatalf("ListEventsByWakeID after canonical: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != "wake.boot_completed" {
		t.Fatalf("rows after canonical cursor = %#v, want completion only", rows)
	}
}

func TestPgStore_CountWakeBootStarted24h_DistinctWakeIDs(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)
	appID := uuid.NewString()
	wakeOne := "wake-count-one-" + uuid.NewString()
	wakeTwo := "wake-count-two-" + uuid.NewString()
	wakeOld := "wake-count-old-" + uuid.NewString()
	data := func(wakeID string) map[string]any {
		return map[string]any{"wake_id": wakeID, "app_id": appID, "trigger": "gateway"}
	}
	now := time.Now().UTC()
	insertWakeTimelineEvent(t, ctx, pool, "schedd", "wake.boot_started", data(wakeOne), now)
	insertWakeTimelineEvent(t, ctx, pool, "vmmd", "wake.boot_started", data(wakeOne), now.Add(time.Millisecond))
	insertWakeTimelineEvent(t, ctx, pool, "schedd", "wake.boot_started", data(wakeTwo), now)
	insertWakeTimelineEvent(t, ctx, pool, "schedd", "wake.boot_started", data(wakeOld), now.Add(-48*time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM events WHERE data->>'app_id' = $1`, appID)
	})

	got, err := s.CountWakeBootStarted24h(ctx, appID)
	if err != nil {
		t.Fatalf("CountWakeBootStarted24h: %v", err)
	}
	if got != 2 {
		t.Fatalf("count = %d, want 2 distinct wakes in trailing 24h", got)
	}
}

func TestPgStore_CountWakeBootStarted24h_EmptyAppIDIsZero(t *testing.T) {
	// The empty-input path must return before touching the pool. This
	// protects the production metrics handler from the `''::uuid`
	// failure reported in issue #2306 and keeps the degraded response
	// contract intact even when a legacy app row is partially populated.
	s := NewPgStore(nil)
	for _, appID := range []string{"", "   "} {
		got, err := s.CountWakeBootStarted24h(context.Background(), appID)
		if err != nil {
			t.Fatalf("appID %q: CountWakeBootStarted24h: %v", appID, err)
		}
		if got != 0 {
			t.Fatalf("appID %q: count = %d, want 0", appID, got)
		}
	}
}

func TestPgStore_CountWakeBootStarted24h_RejectsMalformedAppID(t *testing.T) {
	s := NewPgStore(nil)
	if _, err := s.CountWakeBootStarted24h(context.Background(), "not-a-uuid"); err == nil {
		t.Fatal("CountWakeBootStarted24h malformed app ID returned nil error")
	}
}
