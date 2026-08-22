//go:build !no_pg

// Migration-apply test for 00371 (deployment_audit 90-day backfill
// from events — issue #976 / ADR-124 / SAFE-RELEASES-E.2). Pins:
//  1. The migration set applies cleanly through 00371.
//  2. After applying 00371 (against an empty events table) and
//     then seeding events rows, re-executing the backfill INSERT
//     yields exactly one deployment_audit row per in-scope seed.
//  3. The 90-day cutoff filters out 100-day-old events rows.
//  4. A non-UUID deployment_id is rejected by the regex guard.
//  5. Re-executing the backfill INSERT is idempotent (ON CONFLICT
//     (id) DO NOTHING keeps row count stable across replays).
//
// Build tag mirrors 00318 / 00370.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00374_DeploymentAuditBackfill(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply the full migration set through 00371. The backfill
	// runs against an empty events table (pgtest.Open gives us a
	// clean per-test schema) and produces zero rows.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// Seed three events rows: one in scope, one out of scope by
	// age, one out of scope by malformed deployment_id.
	const deploymentID = "11111111-2222-3333-4444-555555555555"
	_, err := pool.Exec(ctx, `
		INSERT INTO events (at, actor, kind, subject, data) VALUES
		  (now() - INTERVAL '10 days', 'apid', 'app.deployed',
		   '00000000-0000-0000-0000-000000000001'::uuid,
		   jsonb_build_object('deployment_id', $1, 'app_id',
		                      '00000000-0000-0000-0000-000000000001')),
		  (now() - INTERVAL '100 days', 'apid', 'app.deployed',
		   '00000000-0000-0000-0000-000000000002'::uuid,
		   jsonb_build_object('deployment_id',
		                      '22222222-3333-4444-5555-666666666666')),
		  (now() - INTERVAL '5 days', 'apid', 'app.deployed',
		   '00000000-0000-0000-0000-000000000003'::uuid,
		   jsonb_build_object('deployment_id', 'not-a-uuid'))`,
		deploymentID)
	if err != nil {
		t.Fatalf("seed events: %v", err)
	}

	// Re-execute the same backfill INSERT 00371 ships verbatim. We
	// can't replay 00371 itself (it's already applied); the
	// idempotency contract is "running the same INSERT twice
	// produces zero duplicates" — verified by the before/after
	// count check at (5).
	const backfillSQL = `
		INSERT INTO deployment_audit (id, deployment_id, account_id, kind, actor, at, data)
		OVERRIDING SYSTEM VALUE
		SELECT
		    (abs(hashtext(events.id::text))::bigint) AS id,
		    (events.data->>'deployment_id')::uuid AS deployment_id,
		    NULL::uuid AS account_id,
		    CASE events.kind
		        WHEN 'app.deployed'        THEN 'deploy.created'
		        WHEN 'deploy.source_ref'   THEN 'deploy.source_ref'
		        WHEN 'deploy.local_tarball' THEN 'deploy.local_tarball'
		        ELSE NULL
		    END AS kind,
		    events.actor,
		    events.at,
		    events.data
		FROM events
		WHERE events.kind IN ('app.deployed', 'deploy.source_ref', 'deploy.local_tarball')
		  AND events.at >= now() - INTERVAL '90 days'
		  AND (events.data->>'deployment_id') IS NOT NULL
		  AND (events.data->>'deployment_id') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
		  AND CASE events.kind
		          WHEN 'app.deployed'        THEN 'deploy.created'
		          WHEN 'deploy.source_ref'   THEN 'deploy.source_ref'
		          WHEN 'deploy.local_tarball' THEN 'deploy.local_tarball'
		          ELSE NULL
		      END IS NOT NULL
		ON CONFLICT (id) DO NOTHING;`
	if _, err := pool.Exec(ctx, backfillSQL); err != nil {
		t.Fatalf("backfill INSERT: %v", err)
	}

	// (2) Exactly one deployment_audit row for the in-scope seed.
	var inScopeCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_audit
		 WHERE deployment_id = $1::uuid
		   AND kind = 'deploy.created'`,
		deploymentID).Scan(&inScopeCount)
	if err != nil {
		t.Fatalf("query in-scope backfill: %v", err)
	}
	if inScopeCount != 1 {
		t.Errorf("in-scope backfill count = %d, want 1", inScopeCount)
	}

	// (3) The 100-day-old events row is NOT backfilled.
	var outOfScopeAgeCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_audit
		 WHERE deployment_id = '22222222-3333-4444-5555-666666666666'::uuid`).Scan(&outOfScopeAgeCount)
	if err != nil {
		t.Fatalf("query out-of-scope-age backfill: %v", err)
	}
	if outOfScopeAgeCount != 0 {
		t.Errorf("out-of-scope-age backfill count = %d, want 0 (90-day cutoff must filter)", outOfScopeAgeCount)
	}

	// (4) The malformed-UUID events row is NOT backfilled. The
	// regex guard rejects 'not-a-uuid' before the INSERT.
	var outOfScopeShapeCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_audit
		 WHERE kind = 'deploy.created'
		   AND actor = 'apid'
		   AND at > now() - INTERVAL '10 days'
		   AND deployment_id IS NULL`).Scan(&outOfScopeShapeCount)
	if err != nil {
		t.Fatalf("query out-of-scope-shape backfill: %v", err)
	}
	if outOfScopeShapeCount != 0 {
		t.Errorf("out-of-scope-shape backfill count = %d, want 0 (regex guard must reject non-UUID deployment_id)", outOfScopeShapeCount)
	}

	// (5) Replay safety. The deterministic id derivation
	// (abs(hashtext(events.id::text))::bigint) gives every row a
	// stable PK, so the ON CONFLICT (id) DO NOTHING clause is a
	// no-op on a re-run. Replay the backfill a second time and
	// verify zero new rows.
	beforeCount := inScopeCount
	if _, err := pool.Exec(ctx, backfillSQL); err != nil {
		t.Fatalf("replay backfill INSERT: %v", err)
	}
	var afterCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_audit
		 WHERE deployment_id = $1::uuid
		   AND kind = 'deploy.created'`,
		deploymentID).Scan(&afterCount)
	if err != nil {
		t.Fatalf("query replay count: %v", err)
	}
	if afterCount != beforeCount {
		t.Errorf("replay backfill count = %d, want %d (ON CONFLICT (id) DO NOTHING must keep the backfill idempotent)",
			afterCount, beforeCount)
	}
}
