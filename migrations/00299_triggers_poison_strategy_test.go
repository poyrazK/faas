//go:build !no_pg

// Migration-apply test for 00299_triggers_poison_strategy.sql
// (per-trigger Kafka poison-record handling, audit #10 from PR #910).
//
// Pins:
//
//  1. Migration set applies cleanly through 00299 (no goose
//     duplicate-version panic).
//
//  2. The triggers.broker_poison_strategy column exists with TEXT
//     NOT NULL + DEFAULT 'commit' + the closed-vocab CHECK
//     ('commit', 'seek-to-offset'). 'commit' is the previous
//     hardcoded behaviour so existing rows behave identically
//     after the migration lands.
//
//  3. A row INSERTed with broker_poison_strategy='replay' is
//     rejected with SQLSTATE 23514 referencing the column name.
//     A row INSERTed with broker_poison_strategy='seek-to-offset'
//     is accepted. Guards against an off-by-one in either bound
//     of the closed vocabulary.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00299_TriggersPoisonStrategy(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply cleanly.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00298 payload_max and 00299 poison_strategy)", err)
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
		 WHERE table_schema = current_schema() AND table_name = 'triggers' AND column_name = 'broker_poison_strategy'
	`).Scan(&typ, &colDflt, &isNotNull)
	if err != nil {
		t.Fatalf("query broker_poison_strategy column: %v", err)
	}
	if typ != "text" {
		t.Fatalf("broker_poison_strategy type = %q, want text", typ)
	}
	if colDflt == nil || !strings.Contains(*colDflt, "'commit'") {
		t.Fatalf("broker_poison_strategy default = %v, want 'commit'", colDflt)
	}
	if isNotNull != "NO" {
		t.Fatalf("broker_poison_strategy nullable = %q, want NO", isNotNull)
	}

	// (3) Closed-vocab CHECK enforcement.
	acct, app := pinFixtures(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO triggers (account_id, app_id, kind, slug, broker_poison_strategy)
		VALUES ($1, $2, 'queue', $3, 'replay')
	`, acct, app, "poison-floor-test"); err == nil {
		t.Fatalf("broker_poison_strategy='replay' was accepted; closed vocab missing or wrong")
	} else if !strings.Contains(err.Error(), "broker_poison_strategy") {
		t.Fatalf("unexpected error from broker_poison_strategy='replay': %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO triggers (account_id, app_id, kind, slug, broker_poison_strategy)
		VALUES ($1, $2, 'queue', $3, 'seek-to-offset')
	`, acct, app, "poison-ceiling-test"); err != nil {
		t.Fatalf("broker_poison_strategy='seek-to-offset' rejected at upper bound: %v", err)
	}

	// (4) Default preservation: omitting broker_poison_strategy on
	// INSERT must land 'commit'. Catches the case where the
	// DEFAULT clause was dropped during a future rewrite.
	if _, err := pool.Exec(ctx, `
		INSERT INTO triggers (account_id, app_id, kind, slug)
		VALUES ($1, $2, 'queue', $3)
	`, acct, app, "poison-default-test"); err != nil {
		t.Fatalf("default-broker_poison_strategy insert: %v", err)
	}
	var got string
	if err := pool.QueryRow(ctx, `
		SELECT broker_poison_strategy FROM triggers
		 WHERE account_id = $1 AND slug = $2
	`, acct, "poison-default-test").Scan(&got); err != nil {
		t.Fatalf("read back default broker_poison_strategy: %v", err)
	}
	if got != "commit" {
		t.Fatalf("default broker_poison_strategy = %q, want 'commit'", got)
	}
}
