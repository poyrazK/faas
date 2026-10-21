//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

func TestHostingMigrationReplayPreservesAcceptedIntent(t *testing.T) {
	// adr: 202610171453 — replay must preserve private staging, accounting and durable serving intent.
	versions := []int64{
		20261015132102246, 20261015151526707, 20261015161614983, 20261016095936636,
		20261016112658296, 20261016192057719, 20261016195404519, 20261017090210137,
		20261017092513070, 20261017100104546, 20261017102853765, 20261017121650265,
		20261017124228606,
	}
	ctx := t.Context()
	pool := newMigrationReplayPool(t, versions)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO accounts(id,email) VALUES('11111111-1111-4111-8111-111111111111','hosting-replay@example.com');
INSERT INTO orgs(id,slug,name,personal_org,personal_owner_account_id,plan,status) SELECT gen_random_uuid(),'u-111111111111','Personal',true,id,plan,status FROM accounts WHERE id='11111111-1111-4111-8111-111111111111';
INSERT INTO projects(id,account_id,slug) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','hosting-replay');
INSERT INTO project_environments(account_id,project_id,slug) VALUES('11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','production') ON CONFLICT DO NOTHING;
INSERT INTO hosting_sites(id,account_id,project_id,environment,name,hostname,origin_id,durable_routing)
VALUES('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','production','replay','replay.hosting.example',repeat('a',64),true);
INSERT INTO hosting_releases(id,site_id,state,manifest,artifact_bytes,created_at,first_promoted_at,last_activity_at) VALUES
('44444444-4444-4444-8444-444444444444','33333333-3333-4333-8333-333333333333','ready','{"files":[{"size":7}]}',7,'2026-01-01','2026-01-02','2026-01-03'),
('55555555-5555-4555-8555-555555555555','33333333-3333-4333-8333-333333333333','ready','{"files":[{"size":9}]}',9,'2026-01-01',NULL,'2026-01-03');
INSERT INTO hosting_route_publications(site_id,generation,published_generation,published_at,pending_since,last_attempt_at,retry_count,last_error_code)
VALUES('33333333-3333-4333-8333-333333333333',7,6,'2026-01-02','2026-01-03','2026-01-04',5,'publication_unavailable');
INSERT INTO hosting_ci_runs(site_id,id,release_id,preview_id,fingerprint,created_at,preview_expires_at,canceled)
VALUES('33333333-3333-4333-8333-333333333333','66666666-6666-4666-8666-666666666666','55555555-5555-4555-8555-555555555555','77777777-7777-4777-8777-777777777777',repeat('b',64),'2026-01-01','2026-01-02',true);
`); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object(
 'sites',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM hosting_sites s),
 'releases',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM hosting_releases r),
 'routes',(SELECT jsonb_agg(to_jsonb(p) ORDER BY site_id) FROM hosting_route_publications p),
 'runs',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM hosting_ci_runs c))::text`
	var before, after string
	if err := pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	constraints := replayConstraintFingerprint(t, pool)
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=ANY($1::bigint[])`, versions); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal("replay existing hosting intent", err)
	}
	if err := pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("replay changed retained hosting state", err)
	}
	if replayConstraintFingerprint(t, pool) != constraints {
		t.Fatal("replay changed hosting constraint definitions")
	}
}
