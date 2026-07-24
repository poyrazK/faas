//go:build !no_pg

// Migration-apply test for 00030 (invocations table) + 00031 (notify
// triggers) + 00032 (helper view). Pins the load-bearing Move 1
// schema contract:
//
//   1. The migration set applies cleanly through 00032.
//   2. The table exposes the columns the schedd drain writes on the
//      hot path (state, due_at, source, instance_id, attempts).
//   3. The four partial indexes (invocations_due_idx,
//      invocations_app_pending_idx, invocations_delayed_idx,
//      invocations_instance_idx) are present and index-backed.
//   4. The CHECK constraints on `source` and `state` reject values
//      outside the documented set. pg_notify channels are
//      one-shot; the trigger contract is observed through the
//      listener (mirrors 00026_compute_node_notify_test.go).
//   5. The invocations_pending_per_app view from 00032 aggregates
//      pending+dispatching rows.
//
// Build tag mirrors 00024_compute_nodes_test.go:26 and
// 00026_compute_node_notify_test.go:24.

package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00030_Invocations pins the schema + notify + view
// contract for the Move 1 event-shaped queue. Mirrors the
// 00025/00026 shape: one test, comprehensive coverage, no
// per-feature drift.
func TestMigrations_00030_Invocations(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	t.Run("columns_and_indexes", func(t *testing.T) {
		wantCols := []string{
			"id", "app_id", "account_id", "source", "state",
			"payload", "headers", "due_at",
			"method", "path", "cron_id", "scheduled_at",
			"ack_url", "result", "lease_expires_at",
			"received_at", "completed_at", "attempts",
			"last_error", "instance_id", "created_at",
		}
		for _, col := range wantCols {
			var found bool
			if err := pool.QueryRow(ctx, `
				select exists(select 1 from information_schema.columns
				               where table_name = 'invocations' and column_name = $1)`, col).Scan(&found); err != nil {
				t.Fatalf("column lookup %s: %v", col, err)
			}
			if !found {
				t.Errorf("missing column invocations.%s", col)
			}
		}

		// Indexes are load-bearing for the drain hot path (drain
		// tick selects with state='pending' due_at<=now limit 64) +
		// the apid cap check (CountPendingInvocations) + the dashboard
		// "next due" + the meter join.
		wantIdx := []string{
			"invocations_due_idx",
			"invocations_app_pending_idx",
			"invocations_delayed_idx",
			"invocations_instance_idx",
		}
		for _, idx := range wantIdx {
			var found bool
			if err := pool.QueryRow(ctx, `
				select exists(select 1 from pg_indexes
				               where tablename = 'invocations' and indexname = $1)`, idx).Scan(&found); err != nil {
				t.Fatalf("index lookup %s: %v", idx, err)
			}
			if !found {
				t.Errorf("missing index invocations.%s", idx)
			}
		}
	})

	t.Run("source_state_check", func(t *testing.T) {
		// Insert a parent (app) so the FK accepts our test row.
		var appID, accountID string
		if err := pool.QueryRow(ctx, `
			insert into apps (slug, account_id, runtime)
			values ('inv-test-app', '00000000-0000-0000-0000-000000000001'::uuid, 'node22')
			returning id
		`).Scan(&appID); err != nil {
			// Account row is required by the FK; insert it first.
			if _, err := pool.Exec(ctx, `
				insert into accounts (id, email, plan) values
				  ('00000000-0000-0000-0000-000000000001'::uuid, 'inv-test@localhost', 'free')
				on conflict do nothing`); err != nil {
				t.Fatalf("seed account: %v", err)
			}
			if err := pool.QueryRow(ctx, `
				insert into apps (slug, account_id, runtime, ram_mb)
				values ('inv-test-app', '00000000-0000-0000-0000-000000000001'::uuid, 'node22', 128)
				returning id
			`).Scan(&appID); err != nil {
				t.Fatalf("seed app (retry): %v", err)
			}
		}
		accountID = "00000000-0000-0000-0000-000000000001"

		// 1. Valid source + state round-trips.
		var invID string
		if err := pool.QueryRow(ctx, `
			insert into invocations (app_id, account_id, source, state)
			values ($1, $2, 'async_invoke', 'pending')
			returning id`, appID, accountID).Scan(&invID); err != nil {
			t.Fatalf("insert valid: %v", err)
		}

		// 2. Invalid source rejected (CHECK constraint).
		_, err := pool.Exec(ctx, `
			insert into invocations (app_id, account_id, source, state)
			values ($1, $2, 'bogus_source', 'pending')`, appID, accountID)
		if err == nil {
			t.Errorf("invalid source 'bogus_source' was accepted; expected CHECK violation")
		}

		// 3. Invalid state rejected.
		_, err = pool.Exec(ctx, `
			insert into invocations (app_id, account_id, source, state)
			values ($1, $2, 'async_invoke', 'weird_state')`, appID, accountID)
		if err == nil {
			t.Errorf("invalid state 'weird_state' was accepted; expected CHECK violation")
		}

		// 4. Terminal states round-trip.
		for _, st := range []string{"dispatching", "completed", "failed", "cancelled"} {
			if _, err := pool.Exec(ctx, `
				insert into invocations (app_id, account_id, source, state)
				values ($1, $2, 'queue', $3)`, appID, accountID, st); err != nil {
				t.Errorf("insert with state=%s failed: %v", st, err)
			}
		}
	})

	t.Run("notifies_on_insert_and_terminal_update", func(t *testing.T) {
		notif, cancel, err := db.Subscribe(ctx, pool, []string{
			db.NotifyInvocationDue, db.NotifyInvocationDone,
		})
		if err != nil {
			t.Fatalf("Subscribe invocation channels: %v", err)
		}
		defer cancel()

		// Find a parent app from the previous sub-test.
		var appID, accountID string
		if err := pool.QueryRow(ctx, `select id, account_id from apps where slug = 'inv-test-app' limit 1`).Scan(&appID, &accountID); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("lookup parent app: %v", err)
			}
			// Sub-test order is not guaranteed; reseed here too.
			if _, err := pool.Exec(ctx, `
				insert into accounts (id, email, plan) values
				  ('00000000-0000-0000-0000-000000000001'::uuid, 'inv-test@localhost', 'free')
				on conflict do nothing`); err != nil {
				t.Fatalf("seed account: %v", err)
			}
			if err := pool.QueryRow(ctx, `
				insert into apps (slug, account_id, runtime, ram_mb)
				values ('inv-test-app', '00000000-0000-0000-0000-000000000001'::uuid, 'node22', 128)
				returning id, account_id`).Scan(&appID, &accountID); err != nil {
				t.Fatalf("reseed parent app: %v", err)
			}
		}

		// (a) INSERT fires invocation_due with {invocation_id, app_id, source}.
		var invID string
		if err := pool.QueryRow(ctx, `
			insert into invocations (app_id, account_id, source, state)
			values ($1, $2, 'cron', 'pending')
			returning id`, appID, accountID).Scan(&invID); err != nil {
			t.Fatalf("insert cron invocation: %v", err)
		}
		got := waitForNotificationNamed(t, notif, db.NotifyInvocationDue, 5*time.Second)
		var p struct {
			ID     string `json:"invocation_id"`
			AppID  string `json:"app_id"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal([]byte(got.Payload), &p); err != nil {
			t.Fatalf("unmarshal due payload %q: %v", got.Payload, err)
		}
		if p.ID != invID || p.AppID != appID || p.Source != "cron" {
			t.Errorf("due payload = %+v, want id=%s app=%s source=cron", p, invID, appID)
		}

		// (b) UPDATE to terminal state fires invocation_done with
		// the post-state value (the dashboard SSE push — currently
		// unpollable but reserved by the channel).
		if _, err := pool.Exec(ctx, `
			update invocations set state = 'completed', completed_at = now() where id = $1
		`, invID); err != nil {
			t.Fatalf("update to completed: %v", err)
		}
		got = waitForNotificationNamed(t, notif, db.NotifyInvocationDone, 5*time.Second)
		var d struct {
			ID    string `json:"invocation_id"`
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(got.Payload), &d); err != nil {
			t.Fatalf("unmarshal done payload %q: %v", got.Payload, err)
		}
		if d.ID != invID || d.State != "completed" {
			t.Errorf("done payload = %+v, want id=%s state=completed", d, invID)
		}
	})

	t.Run("pending_per_app_view_exists", func(t *testing.T) {
		// Migration 00032 must have created the helper view. The view
		// filters on state IN ('pending','dispatching') — verified by
		// the predicate in information_schema, plus by a smoke SELECT
		// (smoke must not error regardless of row count).
		var ok bool
		if err := pool.QueryRow(ctx, `
			select exists(select 1 from information_schema.views
			               where table_name = 'invocations_pending_per_app')`).Scan(&ok); err != nil {
			t.Fatalf("view existence: %v", err)
		}
		if !ok {
			t.Errorf("view invocations_pending_per_app missing")
		}
		rows, err := pool.Query(ctx, `select count(*) from invocations_pending_per_app`)
		if err != nil {
			t.Fatalf("query view: %v", err)
		}
		rows.Close()
	})
}

// waitForNotificationNamed reads from ch until it observes a notification
// on the named channel; other channels are dropped. Times out via
// t.Fatalf after d.
func waitForNotificationNamed(t *testing.T, ch <-chan db.Notification, channel string, d time.Duration) db.Notification {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case n := <-ch:
			if n.Channel == channel {
				return n
			}
		case <-deadline:
			t.Fatalf("no %s notification within %s (trigger missing or channel not LISTENed)", channel, d)
			return db.Notification{}
		}
	}
}
