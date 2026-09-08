//go:build !no_pg

// Migration-apply test for 00229_edge_rules_kind_geo.sql
// (ADR-091 D21/D22 — kind=geo widener).
//
// Pins:
//
//  1. Migration set applies cleanly through 00229 (no goose
//     duplicate-version panic, no constraint-name collision).
//  2. The CHECK accepts the new value `kind='geo'` (positive
//     round-trip — the abuse-desk customer can land a geo rule).
//  3. All pre-existing kinds still accept (regression guard — a
//     scratchy DROP+ADD rewrite that drops a value would break
//     every production row).
//  4. A typo (`kind='geo_typo'`) is still rejected with pgx 23514
//     (check_violation) — the CHECK is closed and ordered, not
//     over-tolerant.
//  5. The CHECK is named `edge_rules_kind_check` — the auto-name
//     Postgres picks for an inline column CHECK. The companion
//     migration 00229 rewrites this exact name; if a future
//     migration renames the inline CHECK in 00192, this pin +
//     the DROP+ADD in 00229 must update together (silent breakage
//     here means 00229 becomes a no-op).
//
// Seed UUIDs carry the slot number in the last group (`...000192`,
// `...000292`, `...000392`) — mirrors migrations/00192_edge_rules_test.go
// so the parent app fixture is reusable across the two test files.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00229_EdgeRulesKindGeo(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00229. The apply_walk_test.go pin auto-picks
	// up the new slot the next time `make embed-migrations` runs.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 00192 and 00229)", err)
	}

	// Seed the parent account + app so the FK constraints on
	// edge_rules hold. These UUIDs mirror the fixtures seeded by
	// migrations/00192_edge_rules_test.go (the parent app is
	// reused across the two test files so the seed is idempotent
	// under pgtest.Open).
	acctID := "00000000-0000-0000-0000-000000000192"
	appID := "00000000-0000-0000-0000-000000000292"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'edge-rules-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, runtime, status, created_at, ram_mb)
		values ($1, $2, 'edge-rules-test-app', 'node22', 'live', now(), 256)
		on conflict (id) do nothing
	`, appID, acctID); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (2) The new value `kind='geo'` is accepted by the widened CHECK.
	// The action jsonb is a representative EdgeRuleGeoAction shape
	// (mirrors pkg/api/dto.go's EdgeRuleGeoAction — the wire DTO
	// MUST stay in sync with this jsonb literal, otherwise the
	// jsonb round-trip a customer sends fails at the gate).
	geoRuleID := "00000000-0000-0000-0000-000000002701"
	_, err := pool.Exec(ctx, `
		insert into edge_rules (
			id, account_id, app_id, match_host, kind, action
		) values (
			$1, $2, $3, 'geo.example.com', 'geo',
			'{"action":"deny","countries":["US","CA"]}'::jsonb
		)
		on conflict (id) do nothing
	`, geoRuleID, acctID, appID)
	if err != nil {
		t.Fatalf("kind='geo' should be accepted by the widened CHECK, got: %v", err)
	}

	// (3) All pre-existing kinds still accept. A bug in the
	// DROP+ADD rewrite that drops a value would break every
	// production row — this is the regression guard. The list is
	// every value admitted by an EARLIER migration (00214
	// kind=validate, 00219 kind=limit), plus the seven originals
	// from 00192 — ten values total now including 'geo'.
	for _, kind := range []string{"route", "rewrite", "redirect", "headers", "cors", "jwt", "ip", "validate", "limit"} {
		_, err := pool.Exec(ctx, `
			insert into edge_rules (account_id, app_id, match_host, kind, action)
			values ($1, $2, 'regression.example.com', $3, '{}'::jsonb)
		`, acctID, appID, kind)
		if err != nil {
			t.Errorf("kind=%q should still be accepted (regression: pre-existing kind dropped), got: %v", kind, err)
		}
	}

	// (4) Typo rejection — the CHECK is closed, not a prefix match.
	// 'geo_typo' (underscore) MUST still 23514. This pins the
	// enum to the exact ten values, not a glob.
	_, err = pool.Exec(ctx, `
		insert into edge_rules (account_id, app_id, match_host, kind, action)
		values ($1, $2, 'typo.example.com', 'geo_typo', '{}'::jsonb)
	`, acctID, appID)
	var pgErr *pgconn.PgError
	if err == nil {
		t.Errorf("kind='geo_typo' (typo) should be rejected by the closed-vocab CHECK")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("kind='geo_typo' error = %v, want pgx 23514 (check_violation)", err)
	}

	// (5) The CHECK is named `edge_rules_kind_check`. This is the
	// auto-name Postgres assigns to an inline column CHECK on
	// `edge_rules.kind` (Postgres names inline CHECKs as
	// `<table>_<column>_check`). The 00229 migration rewrites
	// this exact name; if a future migration renames the inline
	// CHECK in 00192, this pin + the DROP+ADD in 00229 must update
	// together. Silent breakage here means 00229's DROP IF EXISTS
	// falls through to a no-op and the OLD CHECK (without 'geo')
	// survives — which is exactly the regression this test
	// surfaces.
	var constraintName string
	if err := pool.QueryRow(ctx, `
		SELECT conname FROM pg_constraint
		WHERE conrelid = 'edge_rules'::regclass
		  AND contype = 'c'
		  AND pg_get_constraintdef(oid) LIKE '%kind%IN%'
		ORDER BY conname
		LIMIT 1
	`).Scan(&constraintName); err != nil {
		t.Fatalf("read edge_rules kind CHECK constraint: %v", err)
	}
	if constraintName != "edge_rules_kind_check" {
		t.Errorf("edge_rules kind CHECK named %q, want %q (regression: 00229's DROP+ADD targets this name; if 00192 renames the inline CHECK, both must update together)",
			constraintName, "edge_rules_kind_check")
	}
}
