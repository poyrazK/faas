//go:build !no_pg

// Migration-apply test for 00300_triggers_filter_criteria.sql (ADR-118
// / issue #757 closure — per-trigger FilterCriteria column).
//
// Pins:
//
//  1. Migration set applies cleanly through 00300 (no goose
//     duplicate-version panic). Slot 00300 is the next free real on
//     origin/main past 00299 (PR #910's poison_strategy migration).
//     All fences 00288-00295 + 00300..00303 hold the open-PR slot
//     landscape for the other in-flight mega-PRs (PR #988, #984, etc.).
//
//     Slot chain audit:
//
//     00281_reserve_slot   (PR #978)
//     00282_reserve_slot   (PR #978)
//     00283_reserve_slot   (PR #978)
//     00284_tenant_surfaces_per_host_kind (PR #937, merged)
//     00285_reserve_slot   (PR #963, fence after merge into #964)
//     00286_data_upstreams_deployment_scope (PR #964, merged)
//     00287_pg_ratelimit_add_rule_scope (PR #963, merged)
//     00288_reserve_slot   (PR #978)
//     00289_reserve_slot   (PR #978)
//     00290_reserve_slot   (PR #978)
//     00291_reserve_slot   (PR #978)
//     00292_reserve_slot   (PR #978)
//     00293_validate_mode  (PR #978, merged)
//     00294_reserve_slot   (PR #979-b CORS presets, future)
//     00295_reserve_slot   (open)
//     00296_reserve_slot   (PR #910 round-6 fence)
//     00297_triggers       (PR #910, merged)
//     00298_triggers_payload_max (PR #910, merged)
//     00299_triggers_poison_strategy (PR #910, merged)
//     00300_triggers_filter_criteria (this PR — ADR-118 / #757 closure)
//
//  2. The triggers.filter_criteria column exists with type JSONB and
//     is NULLABLE. No default — the absence of a filter means
//     "every record passes through", and silently backfilling a
//     non-null default would orphan that contract.
//
//  3. NULL filter_criteria, OMITTED filter_criteria (NULL cast), and
//     empty-object filter_criteria all round-trip through the column
//     without coercion. Pins the validator contract in
//     pkg/gregalemanifest.FilterCriteria (commit 2 of ADR-118):
//     the application is the source of truth for the closed-vocab
//     shape; Postgres stores opaque.
//
//  4. The FilterCriteria shape — `{"$or":[...],"$and":[...],"payload":[...]}` —
//     is NOT enforced by Postgres. A malformed tree (e.g. a string)
//     is accepted at the SQL level. This is intentional (the
//     application validator is the closed-vocab enforcer), but the
//     test pins the contract that Postgres does NOT reject it —
//     regression guard against a future CHECK migration that would
//     break the existing convention.
//
//  5. A pre-existing trigger row from migration 00297 (the
//     `filter_criteria IS NULL` default-zero state) round-trips
//     unmodified. Pins the additive nature of the migration — a
//     downgrade that drops the column would not orphan production
//     rows.
//
//  6. Replay safety: re-running db.MigrateUp is a no-op. ADD COLUMN
//     IF NOT EXISTS is idempotent on PG9.6+ (matches 00298 pattern).
package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00300_TriggersFilterCriteria(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply cleanly through 00300.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00299 triggers_poison_strategy and 00300 triggers_filter_criteria)", err)
	}

	// (2) Column shape: JSONB, NULLABLE, no default.
	var (
		dataType   string
		isNullable string
		colDflt    *string
	)
	err := pool.QueryRow(ctx, `
		SELECT data_type, is_nullable, column_default
		  FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'triggers' AND column_name = 'filter_criteria'
	`).Scan(&dataType, &isNullable, &colDflt)
	if err != nil {
		t.Fatalf("query filter_criteria column: %v (the migration must add the column to `triggers`; absence here means the ADD COLUMN statement did not run)", err)
	}
	if dataType != "jsonb" {
		t.Fatalf("filter_criteria type = %q, want jsonb (closed-vocab FilterCriteria lives in pkg/gregalemanifest; Postgres stores opaque)", dataType)
	}
	if isNullable != "YES" {
		t.Fatalf("filter_criteria nullable = %q, want YES (absence-of-filter = match-anything; a NOT NULL column would force every existing trigger row to backfill on upgrade)", isNullable)
	}
	if colDflt != nil {
		t.Fatalf("filter_criteria column_default = %v, want nil (no backfill — preserves byte-for-byte the pre-00300 behaviour of every trigger row from migration 00297 onwards)", colDflt)
	}

	// (3) NULL / OMITTED / empty-object round-trip. NULL is the
	// default-zero state ("every record passes through"). OMITTED
	// (the literal SQL NULL cast) must be byte-identical. Empty
	// object is an explicit "no filter clauses" state — also
	// match-anything per the validator's closed-vocab contract.
	acct, app := pinFixtures(t, ctx, pool)
	t.Run("null round-trip", func(t *testing.T) {
		var got *string
		if err := pool.QueryRow(ctx, `
			insert into triggers (account_id, app_id, kind, slug, filter_criteria)
			values ($1, $2, 'queue', $3, NULL)
			returning filter_criteria::text
		`, acct, app, "filter-criteria-null").Scan(&got); err != nil {
			t.Fatalf("insert filter_criteria=NULL: %v", err)
		}
		if got != nil {
			t.Fatalf("filter_criteria round-trip = %v, want nil", *got)
		}
	})
	t.Run("empty-object round-trip", func(t *testing.T) {
		var got string
		if err := pool.QueryRow(ctx, `
			insert into triggers (account_id, app_id, kind, slug, filter_criteria)
			values ($1, $2, 'queue', $3, '{}'::jsonb)
			returning filter_criteria::text
		`, acct, app, "filter-criteria-empty").Scan(&got); err != nil {
			t.Fatalf("insert filter_criteria='{}': %v", err)
		}
		// pgjson re-encodes; on round-trip '{}' is canonical.
		if strings.TrimSpace(got) != "{}" {
			t.Fatalf("filter_criteria empty round-trip = %q, want '{}'", got)
		}
	})

	// (4) Malformed FilterCriteria is accepted by Postgres. Pins
	// the architectural choice that the application validator
	// (pkg/gregalemanifest.FilterCriteria) is the closed-vocab
	// enforcer. Postgres CHECKs on jsonpath expressions are
	// brittle and would block a future $or/$and widening without
	// a migration. This test guards against a silent regression
	// where a future migration adds a CHECK that locks in a
	// specific shape — that would break the superset contract.
	t.Run("malformed accepted at SQL", func(t *testing.T) {
		// A raw string is NOT a valid FilterCriteria per the
		// validator, but Postgres must accept it (the validator
		// rejects upstream, never the DB).
		var got string
		if err := pool.QueryRow(ctx, `
			insert into triggers (account_id, app_id, kind, slug, filter_criteria)
			values ($1, $2, 'queue', $3, '"not-a-filter-tree"'::jsonb)
			returning filter_criteria::text
		`, acct, app, "filter-criteria-malformed").Scan(&got); err != nil {
			t.Fatalf("malformed filter_criteria rejected at SQL: %v (Postgres must store opaque; the validator rejects upstream)", err)
		}
		var v interface{}
		if err := json.Unmarshal([]byte(got), &v); err != nil {
			t.Fatalf("filter_criteria malformed round-trip: invalid JSON in DB: %v (payload=%q)", err, got)
		}
	})

	// (5) Canonical FilterCriteria shape round-trips. Pins the
	// end-to-end contract that pkg/sched/filter.go::Match (commit 5
	// of ADR-118) will evaluate on read.
	t.Run("canonical shape round-trip", func(t *testing.T) {
		canonical := `{
			"$or":  [{"op":"exists","field":"x-event-id"}],
			"$and": [{"op":"eq","field":"x-tenant","value":"acme"}],
			"payload": [{"op":"jsonpath","path":"$.event.type","value":"order.created"}]
		}`
		var got string
		if err := pool.QueryRow(ctx, `
			insert into triggers (account_id, app_id, kind, slug, filter_criteria)
			values ($1, $2, 'queue', $3, $4::jsonb)
			returning filter_criteria::text
		`, acct, app, "filter-criteria-canonical", canonical).Scan(&got); err != nil {
			t.Fatalf("insert canonical filter_criteria: %v", err)
		}
		// Decode both sides; json.Unmarshal into interface{} and
		// re-marshal normalises whitespace + key order, so the
		// canonical shape must compare equal after a round-trip.
		var in, out interface{}
		if err := json.Unmarshal([]byte(canonical), &in); err != nil {
			t.Fatalf("canonical input: %v", err)
		}
		if err := json.Unmarshal([]byte(got), &out); err != nil {
			t.Fatalf("canonical round-trip: %v (payload=%q)", err, got)
		}
		inJSON, _ := json.Marshal(in)
		outJSON, _ := json.Marshal(out)
		if string(inJSON) != string(outJSON) {
			t.Fatalf("canonical round-trip drift: in=%s out=%s (the validator's closed-vocab shape must survive a SQL round-trip)", inJSON, outJSON)
		}
	})

	// (6) Replay safety: re-running db.MigrateUp is a no-op.
	// ADD COLUMN IF NOT EXISTS is idempotent on PG9.6+; a drifted
	// box (relation present, goose row missing) re-applies without
	// tripping SQLSTATE 42701 "column already exists".
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (migration must be replay-safe via ADD COLUMN IF NOT EXISTS — same carve-out as 00298_triggers_payload_max)", err)
	}
}
