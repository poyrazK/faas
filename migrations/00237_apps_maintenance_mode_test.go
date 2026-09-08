//go:build !no_pg

// Migration-apply test for 00237_apps_maintenance_mode.sql
// (ADR-091 amendment — D18 per-kind extension, PR-A fence + PR-B
// runtime + PR-C rollout-closer).
//
// Pins:
//
//  1. Migration set applies cleanly through 00237 (no goose
//     duplicate-version panic).
//  2. apps.maintenance_mode column exists, type boolean,
//     NOT NULL, DEFAULT false. The runtime surface uses the
//     Set-bit convention (`*bool` in DTOs, `case when $N then $M
//     else maintenance_mode end` in pgstore); a NULL default
//     would silently break that convention.
//  3. Partial index `apps_maintenance_mode_idx` exists with the
//     predicate `WHERE maintenance_mode = true` (mirror
//     apps_route_metrics_enabled_idx at slot 00216).
//  4. The trigger `apps_maintenance_mode_notify` exists AFTER
//     UPDATE on apps. The trigger emits pg_notify('app_changed',
//     NEW.id::text) ONLY when maintenance_mode IS DISTINCT FROM
//     old.maintenance_mode. Pins:
//     - Trigger exists, AFTER UPDATE, on apps
//     - Trigger function body matches the expected pg_notify
//     shape (substring pin, not regex)
//  5. Positive round-trip: insert an app, set
//     maintenance_mode=true, read it back, assert the value
//     round-trips.
//  6. The trigger fires ONLY on maintenance_mode changes:
//     - Update with maintenance_mode unchanged → no notification
//     - Update with maintenance_mode flip → notification with payload
//     'NEW.id::text'
//  7. Replay safety: re-running db.MigrateUp is a no-op
//     (`ADD COLUMN IF NOT EXISTS` + `CREATE INDEX IF NOT
//     EXISTS` + `CREATE OR REPLACE FUNCTION` + `DROP TRIGGER IF
//     EXISTS` all keep the second pass silent).
package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00237_AppsMaintenanceMode(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00237 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00236 kind=maintenance and 00237 apps.maintenance_mode)", err)
	}

	// (2) Column shape.
	var (
		dataType      string
		isNullable    string
		columnDefault *string
	)
	err := pool.QueryRow(ctx, `
		select data_type, is_nullable, column_default
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'apps'
		   and column_name = 'maintenance_mode'
	`).Scan(&dataType, &isNullable, &columnDefault)
	if err != nil {
		t.Fatalf("query apps.maintenance_mode column: %v (the 00237 ADD COLUMN IF NOT EXISTS must have landed)", err)
	}
	if dataType != "boolean" {
		t.Errorf("apps.maintenance_mode data_type = %q, want 'boolean'", dataType)
	}
	if isNullable != "NO" {
		t.Errorf("apps.maintenance_mode is_nullable = %q, want 'NO' (NOT NULL constraint)", isNullable)
	}
	if columnDefault == nil || !strings.Contains(*columnDefault, "false") {
		t.Errorf("apps.maintenance_mode column_default = %v, want a default of 'false'", columnDefault)
	}

	// (3) Partial index.
	var indexDef string
	err = pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'apps'
		   and indexname = 'apps_maintenance_mode_idx'
	`).Scan(&indexDef)
	if err != nil {
		t.Fatalf("query apps_maintenance_mode_idx: %v (the 00237 CREATE INDEX IF NOT EXISTS must have landed)", err)
	}
	if !strings.Contains(indexDef, "WHERE") || !strings.Contains(indexDef, "maintenance_mode") || !strings.Contains(indexDef, "true") {
		t.Errorf("apps_maintenance_mode_idx indexdef = %q, want partial predicate referencing maintenance_mode and true", indexDef)
	}

	// (4) Trigger exists, AFTER UPDATE, on apps.
	var triggerEvent string
	var triggerName string
	err = pool.QueryRow(ctx, `
		select trigger_name, event_manipulation
		  from information_schema.triggers
		 where event_object_schema = current_schema()
		   and event_object_table = 'apps'
		   and trigger_name = 'apps_maintenance_mode_notify'
	`).Scan(&triggerName, &triggerEvent)
	if err != nil {
		t.Fatalf("query apps_maintenance_mode_notify trigger: %v (the 00237 CREATE TRIGGER must have landed)", err)
	}
	if triggerName != "apps_maintenance_mode_notify" {
		t.Errorf("trigger_name = %q, want 'apps_maintenance_mode_notify'", triggerName)
	}
	if !strings.EqualFold(triggerEvent, "UPDATE") {
		t.Errorf("trigger event_manipulation = %q, want 'UPDATE' (the trigger fires on AFTER UPDATE)", triggerEvent)
	}

	// pg_proc.prosrc pin. The trigger and its function share the
	// name `apps_maintenance_mode_notify` (see 00237_apps_maintenance_mode.sql
	// and schema.sql) — resolve the function through pg_trigger so
	// this stays pinned to whatever function the trigger actually
	// executes, even if a later migration renames it.
	var proName, proSrc string
	err = pool.QueryRow(ctx, `
		select p.proname, p.prosrc
		  from pg_trigger tg
		  join pg_class c on c.oid = tg.tgrelid
		  join pg_proc p on p.oid = tg.tgfoid
		 where c.relnamespace = current_schema()::regnamespace
		   and c.relname = 'apps'
		   and tg.tgname = 'apps_maintenance_mode_notify'
		   and not tg.tgisinternal
	`).Scan(&proName, &proSrc)
	if err != nil {
		t.Fatalf("query apps_maintenance_mode_notify trigger function body: %v", err)
	}
	if proName != "apps_maintenance_mode_notify" {
		t.Errorf("trigger function name = %q, want 'apps_maintenance_mode_notify'", proName)
	}
	for _, want := range []string{
		"pg_notify",
		"'app_changed'",
		"NEW.id",
		"maintenance_mode",
		"IS DISTINCT FROM",
	} {
		if !strings.Contains(proSrc, want) {
			t.Errorf("trigger function body missing %q (got: %s)", want, proSrc)
		}
	}

	// (5) Positive round-trip.
	accountID := "00000000-0000-0000-0000-000000002237"
	appID := "00000000-0000-0000-0000-000000012237"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'maintenance-mode-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'maintenance-mode-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	var maintenanceMode bool
	if err := pool.QueryRow(ctx, `
		select maintenance_mode
		  from apps
		 where id = $1
	`, appID).Scan(&maintenanceMode); err != nil {
		t.Fatalf("read default maintenance_mode: %v", err)
	}
	if maintenanceMode {
		t.Errorf("default maintenance_mode = true, want false (NOT NULL DEFAULT false)")
	}

	// (6) Trigger fires ONLY on maintenance_mode changes.
	// Use db.Subscribe to LISTEN on app_changed, then issue
	// updates and assert the right shape of notifications.
	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyAppChanged})
	if err != nil {
		t.Fatalf("Subscribe(app_changed): %v", err)
	}
	defer cancel()

	// Flip to true and read back.
	if _, err := pool.Exec(ctx, `
		update apps
		   set maintenance_mode = true
		 where id = $1
	`, appID); err != nil {
		t.Fatalf("update maintenance_mode=true: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select maintenance_mode
		  from apps
		 where id = $1
	`, appID).Scan(&maintenanceMode); err != nil {
		t.Fatalf("read maintenance_mode after update: %v", err)
	}
	if !maintenanceMode {
		t.Errorf("after UPDATE maintenance_mode = false, want true (round-trip broken)")
	}

	// Expect the maintenance_mode flip notification. Filter
	// parallel-pgtest schemas' payloads (cluster-global LISTEN):
	// only ours carries the seeded appID verbatim.
	got := waitForMaintenanceNotification(t, notif, appID, 5*time.Second)
	if got.Payload != appID {
		t.Errorf("notification payload = %q, want %q (the trigger body emits NEW.id::text)", got.Payload, appID)
	}

	// Unrelated update (no maintenance_mode change) → no
	// notification expected. Drain the channel with a short
	// window and assert no NEW appID notification arrives.
	notif2, cancel2, err := db.Subscribe(ctx, pool, []string{db.NotifyAppChanged})
	if err != nil {
		t.Fatalf("re-subscribe app_changed: %v", err)
	}
	defer cancel2()

	if _, err := pool.Exec(ctx, `
		update apps
		   set slug = 'maintenance-mode-test-renamed'
		 where id = $1
	`, appID); err != nil {
		t.Fatalf("unrelated update: %v", err)
	}
	select {
	case n := <-notif2:
		t.Errorf("unrelated update emitted notification %q (the trigger must fire ONLY when maintenance_mode IS DISTINCT FROM old)", n.Payload)
	case <-time.After(500 * time.Millisecond):
		// Expected: no notification.
	}

	// (7) Replay safety.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (ADD COLUMN IF NOT EXISTS / CREATE INDEX IF NOT EXISTS / CREATE OR REPLACE FUNCTION / DROP TRIGGER IF EXISTS guards must keep the second pass a no-op)", err)
	}
}

// waitForMaintenanceNotification blocks up to d for the next entry
// on the notification channel whose payload equals want verbatim.
// The maintenance_mode_notify trigger emits NEW.id::text as the
// payload (no JSON envelope). pg_notify is cluster-global, so a
// sibling schema's maintenance_mode flip could leak in — we filter
// by exact payload match.
func waitForMaintenanceNotification(t *testing.T, ch <-chan db.Notification, want string, d time.Duration) db.Notification {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case n := <-ch:
			if n.Payload == want {
				return n
			}
			// Drop and keep waiting.
		case <-deadline:
			t.Fatalf("no app_changed notification with payload %q within %s (trigger missing, channel not LISTENed, or sibling schema's payload flooded the queue)", want, d)
			return db.Notification{}
		}
	}
}
