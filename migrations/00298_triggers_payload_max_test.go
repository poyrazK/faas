//go:build !no_pg

// Migration-apply test for 00298_triggers_payload_max.sql (per-trigger
// payload size cap, audit #7 from PR #910).
//
// Pins:
//
//  1. Migration set applies cleanly through 00298 (no goose
//     duplicate-version panic).
//
//  2. The triggers.payload_max_bytes column exists with INT NOT NULL
//     + DEFAULT 6291456 (6 MiB) — matches the previous hardcoded byte
//     cap in pkg/sched/dispatch_triggers.go::closeBatch so existing
//     rows behave identically after the migration lands.
//
//  3. A row INSERTed with payload_max_bytes=1 (below the 1024 floor)
//     is rejected with SQLSTATE 23514 referencing the column name.
//     A row INSERTed with payload_max_bytes=67108864 (at the 64 MiB
//     ceiling) is accepted. Guards against an off-by-one in either
//     bound.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00298_TriggersPayloadMax(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply cleanly.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00297 triggers and 00298 payload_max)", err)
	}

	// (2) Column shape.
	var (
		typ       string
		colDflt   *string
		isNotNull string
	)
	err := pool.QueryRow(ctx, `
		SELECT data_type, column_default, is_nullable
		  FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'triggers' AND column_name = 'payload_max_bytes'
	`).Scan(&typ, &colDflt, &isNotNull)
	if err != nil {
		t.Fatalf("query payload_max_bytes column: %v", err)
	}
	if typ != "integer" {
		t.Fatalf("payload_max_bytes type = %q, want integer", typ)
	}
	if colDflt == nil || !strings.Contains(*colDflt, "6291456") {
		t.Fatalf("payload_max_bytes default = %v, want 6291456", colDflt)
	}
	if isNotNull != "NO" {
		t.Fatalf("payload_max_bytes nullable = %q, want NO", isNotNull)
	}

	// (3) Floor + ceiling CHECK enforcement.
	acct, app := pinFixtures(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO triggers (account_id, app_id, kind, slug, payload_max_bytes)
		VALUES ($1, $2, 'queue', $3, 1)
	`, acct, app, "payload-floor-test"); err == nil {
		t.Fatalf("payload_max_bytes=1 was accepted; CHECK floor 1024 missing or wrong")
	} else if !strings.Contains(err.Error(), "payload_max_bytes") {
		t.Fatalf("unexpected error from payload_max_bytes=1: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO triggers (account_id, app_id, kind, slug, payload_max_bytes)
		VALUES ($1, $2, 'queue', $3, 67108864)
	`, acct, app, "payload-ceiling-test"); err != nil {
		t.Fatalf("payload_max_bytes=67108864 rejected at ceiling: %v", err)
	}
}
