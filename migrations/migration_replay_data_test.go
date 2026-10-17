//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

func TestMigrationReplayPreservesSavedProgress(t *testing.T) {
	// adr:1573,1575,1578,1579 — ledger recovery preserves accepted work and fixed deadlines.
	versions := []int64{
		20261017190925552, 20261017190925559, 20261017190925564,
		20261017190925569, 20261017190925575, 20261017190925580, 20261017190925585,
	}
	ctx := t.Context()
	pool := newMigrationReplayPool(t, versions)
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	deployment := seedDeployment(t, ctx, pool, app, "pending")
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := now.Add(time.Minute)
	nextAttempt := now.Add(3 * time.Minute)
	const verificationID = "11111111-1111-4111-8111-111111111111"
	const cancelledID = "22222222-2222-4222-8222-222222222222"
	const cutoverID = "33333333-3333-4333-8333-333333333333"
	const migrationID = "44444444-4444-4444-8444-444444444444"
	for _, receipt := range []struct{ id, status, phase string }{
		{verificationID, "pending", "dependencies"},
		{cancelledID, "cancelled", "complete"},
	} {
		snapshot, err := json.Marshal(map[string]any{
			"Report":     map[string]string{"id": receipt.id, "status": receipt.status, "phase": receipt.phase},
			"LeaseUntil": lease,
		})
		if err != nil {
			t.Fatal(err)
		}
		var due any
		if receipt.status == "pending" {
			due = nextAttempt
		}
		if _, err := pool.Exec(ctx, `INSERT INTO migration_verifications
 (id,account_id,app_id,created_at,version,snapshot,next_attempt_at)
 VALUES ($1,$2,$3,$4,7,$5::jsonb,$6)`, receipt.id, account, app, now, snapshot, due); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO migration_cutovers
 (id,account_id,app_id,created_at,version,snapshot,next_attempt_at)
 VALUES ($1::uuid,$2,$3,$4,5,jsonb_build_object('Report',jsonb_build_object('id',$1::uuid,'status','checking')),$5)`, cutoverID, account, app, now, nextAttempt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project_migrations
 (id,account_id,project_slug,created_at,version,lease_until,snapshot)
 VALUES ($1::uuid,$2,'replay',$3,9,$4,jsonb_build_object('Report',jsonb_build_object(
 'id',$1::uuid,'project','replay','status','tracking','services',jsonb_build_array(
 jsonb_build_object('name','api','status','live','deployment_id',$5::uuid)))))`, migrationID, account, now, lease, deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_public_build_urls(deployment_id,urls)
 VALUES ($1,'{"VITE_API_URL":"https://api.example.com"}'::jsonb)`, deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_build_dependency_gates(deployment_id,progress,check_after)
 VALUES ($1::uuid,jsonb_build_object('dependencies',jsonb_build_array(jsonb_build_object(
 'service','api','app_id',$2::uuid,'deployment_id',$1::uuid)),
 'status','waiting','deadline_at',$3::timestamptz,'check_after',$4::timestamptz),$4::timestamptz)`, deployment, app, now.Add(20*time.Minute), nextAttempt); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object(
 'verifications',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM migration_verifications r),
 'cutovers',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM migration_cutovers r),
 'migrations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM project_migrations r),
 'public_urls',(SELECT jsonb_agg(to_jsonb(r) ORDER BY deployment_id) FROM deployment_public_build_urls r),
 'build_gates',(SELECT jsonb_agg(to_jsonb(r) ORDER BY deployment_id) FROM deployment_build_dependency_gates r))::text`
	var before, after string
	if err := pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	constraints := replayConstraintFingerprint(t, pool)
	deleted, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=ANY($1::bigint[])`, versions)
	if err != nil || deleted.RowsAffected() != int64(len(versions)) {
		t.Fatalf("remove exactly the migration ledger tail: rows=%d err=%v", deleted.RowsAffected(), err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal("replay saved migration progress", err)
	}
	if err := pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatalf("replay changed saved migration progress or deadlines: %v", err)
	}
	if replayConstraintFingerprint(t, pool) != constraints {
		t.Fatal("replay changed migration constraints")
	}
}
