//go:build !no_pg

// Migration-apply test for 00264_deployments_secret_findings.sql
// (secret-scan v2 audit row).
//
// Pins:
//
//  1. Migration set applies cleanly through 00264 (no goose
//     duplicate-version panic). Slot 00264 is the next free real
//     after PR #845 (00229_edge_rules_kind_geo), PR #867
//     (00231_edge_rules_kind_maintenance, 00232_apps_maintenance_mode),
//     and PR #864 (00232_edge_rules_kind_budget). Future renumbering
//     must re-verify `git ls-tree origin/main migrations/` after every
//     rebase, per migration-test-uuid-sed-residual and
//     pr-867-maintenance-cluster-slot-chase.
//
//  2. Both new columns exist on the `deployments` table with the
//     expected types + nullability:
//     secret_findings   jsonb NOT NULL DEFAULT '[]'::jsonb
//     secret_scanned_at timestamptz NULL
//     Pins that the wire-side `SecretScanResult{Status, ScannedAt,
//     Findings}` shape in cmd/apid/handlers_ext.go can hydrate
//     without a SELECT-side fallback (e.g. a NULL-on-read at the
//     Go layer).
//
//  3. The deployments_scan_status_chk CHECK was widened to
//     include the new 'complete_with_redactions' value. Distinct
//     from 'complete' (scan completed and emitted no redactions).
//     Pins the closed-set contract so a future CHECK rewrite
//     doesn't silently drop the new value.
//
//  4. Positive round-trip: insert a deployment row with
//     scan_status='complete_with_redactions' and a
//     secret_findings jsonb array. Read it back. Pins the
//     end-to-end contract that the apid side will rely on after
//     the secret-scan rejection fires.
//
//  5. Replay safety: re-running db.MigrateUp is a no-op. The
//     apply_walk_test harness pins this at the directory level;
//     per-migration shape is asserted here as defence in depth.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00264_DeploymentsSecretFindings(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00264 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 00220 maintenance and 00264 secret-findings)", err)
	}

	// (2) Column shape — both new columns exist with the right types.
	// We pull the column metadata directly from pg_attribute +
	// pg_type + pg_namespace so the test pins the wire contract the
	// Go SELECT projection (pkg/state/pgstore.go) depends on.
	rows, err := pool.Query(ctx, `
		select a.attname, t.typname, a.attnotnull
		  from pg_attribute a
		  join pg_type      t on t.oid = a.atttypid
		  join pg_class     c on c.oid = a.attrelid
		  join pg_namespace n on n.oid = c.relnamespace
		 where n.nspname = current_schema()
		   and c.relname = 'deployments'
		   and a.attnum > 0
		   and a.attname in ('secret_findings', 'secret_scanned_at')
		 order by a.attname`)
	if err != nil {
		t.Fatalf("query deployments columns: %v", err)
	}
	defer rows.Close()
	got := map[string]struct {
		typ     string
		notNull bool
	}{}
	for rows.Next() {
		var name, typ string
		var notNull bool
		if err := rows.Scan(&name, &typ, &notNull); err != nil {
			t.Fatalf("scan column row: %v", err)
		}
		got[name] = struct {
			typ     string
			notNull bool
		}{typ, notNull}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate deployments columns: %v", err)
	}
	// secret_findings must be jsonb NOT NULL (the `'[]'::jsonb`
	// default covers the pre-feature rows).
	if c, ok := got["secret_findings"]; !ok {
		t.Errorf("deployments.secret_findings missing (00264 must have ADD COLUMN)")
	} else if c.typ != "jsonb" {
		t.Errorf("deployments.secret_findings type = %q, want jsonb", c.typ)
	} else if !c.notNull {
		t.Errorf("deployments.secret_findings must be NOT NULL (the '[]'::jsonb default covers pre-feature rows)")
	}
	// secret_scanned_at must be timestamptz NULL (nullable; NULL
	// pre-00264, set when apid writes the audit row).
	if c, ok := got["secret_scanned_at"]; !ok {
		t.Errorf("deployments.secret_scanned_at missing (00264 must have ADD COLUMN)")
	} else if c.typ != "timestamptz" {
		t.Errorf("deployments.secret_scanned_at type = %q, want timestamptz", c.typ)
	} else if c.notNull {
		t.Errorf("deployments.secret_scanned_at must be NULLABLE (NULL on pre-00264 rows; apid sets it on the audit path)")
	}

	// (3) deployments_scan_status_chk CHECK was widened to include
	// 'complete_with_redactions'. Pin the closed-set vocabulary
	// so a future CHECK rewrite that drops the new value trips here
	// before production.
	var def string
	err = pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployments_scan_status_chk'
		   and n.nspname = current_schema()`).Scan(&def)
	if err != nil {
		t.Fatalf("query deployments_scan_status_chk constraint: %v (the 00264 widening must have landed)", err)
	}
	// Substring pin: pg_get_constraintdef emits either `IN (...)` or
	// `= ANY (ARRAY[...])` per pg-get-constraintdef-shapes.md. Assert
	// the new value appears as a quoted literal so 'complete' does
	// not match 'complete_with_redactions' accidentally.
	if !strings.Contains(def, "'complete_with_redactions'") {
		t.Errorf("deployments_scan_status_chk missing 'complete_with_redactions' in def %q (00264 must have widened the closed set)", def)
	}
	// Belt-and-braces: the four pre-00264 values must still be present.
	for _, v := range []string{"pending", "complete", "failed", "skipped"} {
		if !strings.Contains(def, "'"+v+"'") {
			t.Errorf("deployments_scan_status_chk lost %q during the 00264 widening (def %q)", v, def)
		}
	}

	// (4) Positive round-trip: insert a deployment row stamped with
	// the new scan_status + a findings jsonb array, read it back.
	// We seed the parent account + app + deployment in the same
	// pgtest schema (each run gets its own schema, so the UUIDs
	// are unique enough at 00264-prefix).
	const (
		accountID    = "00000000-0000-0000-0000-000000002233"
		appID        = "00000000-0000-0000-0000-000000012233"
		deploymentID = "00000000-0000-0000-0000-000000022233"
	)
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ($1, 'scale', 'secret-findings-test@example.com')
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, $2, 'secret-findings-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`, appID, accountID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	findingsJSON := `[{"file":".env.production","line":2,"key":"STRIPE_SECRET_KEY","provider":"stripe_live","severity":"high","snippet":"sk_liv…XXXX"}]`
	// `deployments` has no `source` / `source_kind` columns: the upload
	// origin is `source_path` (+ source_url/source_bytes) and the deploy
	// flavour is `kind` (image|tarball|dockerfile|github|preview). Both
	// `image_digest` and `status` are NOT NULL with no default, so the
	// row has to carry them.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, kind, source_path, status, scan_status, secret_findings, secret_scanned_at, created_at)
		values ($1, $2, 'sha256:' || repeat('b', 64), 'tarball', 'local', 'pending', 'complete_with_redactions', $3::jsonb, now(), now())
		on conflict (id) do nothing
	`, deploymentID, appID, findingsJSON); err != nil {
		t.Fatalf("insert deployment: %v (00264 must accept scan_status='complete_with_redactions' + secret_findings jsonb)", err)
	}
	var gotStatus string
	var gotFindings []byte
	if err := pool.QueryRow(ctx, `
		select scan_status, secret_findings::text
		  from deployments
		 where id = $1
	`, deploymentID).Scan(&gotStatus, &gotFindings); err != nil {
		t.Fatalf("read deployment: %v", err)
	}
	if gotStatus != "complete_with_redactions" {
		t.Errorf("scan_status = %q, want complete_with_redactions", gotStatus)
	}
	if !strings.Contains(string(gotFindings), "stripe_live") {
		t.Errorf("secret_findings = %s, want to contain 'stripe_live' (the wire-shape contract is jsonb list of Finding objects)", string(gotFindings))
	}
	if !strings.Contains(string(gotFindings), "sk_liv") {
		t.Errorf("secret_findings = %s, want snippet prefix 'sk_liv' (the safe-snippet policy pins first-6)", string(gotFindings))
	}

	// (5) Replay safety — re-run MigrateUp; the IF EXISTS/IF NOT
	// EXISTS guards must make this a no-op. apply_walk_test pins
	// this at the directory level; this is the per-migration belt
	// and braces.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp (replay): %v (00264 must be replay-safe; the IF EXISTS/IF NOT EXISTS guards cover every ALTER)", err)
	}
}
