//go:build !no_pg

package migrations_test

// adr: 1591 — replay preserves commercial evidence and later email constraints.

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProductControlMigrationReplayPreservesCommercialRows(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	seedProductControlReplay(t, pool)
	beforeRows := productControlReplayRows(t, pool)
	beforeConstraints := replayConstraintFingerprint(t, pool)
	sources, err := migrations.Sources()
	if err != nil {
		t.Fatal(err)
	}
	var versions []int64
	for _, source := range sources {
		if strings.Contains(source.Filename, "_product_control") {
			versions = append(versions, source.Version)
		}
	}
	if len(versions) == 0 {
		t.Fatal("no Product Control migrations were selected")
	}
	tag, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id = ANY($1::bigint[])", versions)
	if err != nil || tag.RowsAffected() != int64(len(versions)) {
		t.Fatalf("remove every product ledger entry: %v %v", tag, err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatalf("replay populated Product Control tables: %v", err)
	}
	if productControlReplayRows(t, pool) != beforeRows {
		t.Fatal("replay changed commercial evidence or personal email state")
	}
	if replayConstraintFingerprint(t, pool) != beforeConstraints {
		t.Fatal("replay changed the final constraint definitions")
	}
}

func seedProductControlReplay(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
INSERT INTO accounts(id,email,plan) VALUES ('10000000-0000-4000-8000-000000000001','product-replay@example.test','pro');
INSERT INTO apps(id,account_id,slug,ram_mb,max_concurrency,idle_timeout_s,status)
 VALUES ('10000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000001','replay-product',256,1,30,'active');
INSERT INTO platform_tenants(id,account_id,external_ref,name)
 VALUES ('10000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000001','replay-customer','Replay customer');
INSERT INTO product_control_revisions(account_id,app_id,revision,spec)
 VALUES ('10000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000002',repeat('a',64),'{}');
INSERT INTO product_control_receipts(operation_id,account_id,app_id,tenant_id,revision,feature,plan,currency,period,unit_price_millicents,admitted_at,succeeded_at,resolved_state,resolved_at)
 VALUES ('10000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000003',repeat('a',64),'export','starter','EUR','2026-10',25000,'2026-10-01T00:00:00Z','2026-10-01T00:01:00Z','succeeded','2026-10-01T00:01:00Z');
INSERT INTO product_control_email_subscriptions(id,account_id,app_id,tenant_id,auth_app_id,organization_id,actor_id,app_slug,auth_slug,version,work_outcomes,help_replies,work_since,help_since,grant_sealed,scan_cursor)
 VALUES ('10000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000007','10000000-0000-4000-8000-000000000008','10000000-0000-4000-8000-000000000009','replay-product','replay-auth',1,false,false,now(),now(),'','');
INSERT INTO product_control_email_deliveries(id,subscription_id,preference_version,source_revision,notification,status,reason,attempts,created_at,updated_at,next_at,deadline)
 VALUES ('10000000-0000-4000-8000-000000000006','10000000-0000-4000-8000-000000000005',1,repeat('b',64),'{}','cancelled','unsubscribed',0,now(),now(),now(),now()+interval '1 day');
CREATE FUNCTION product_replay_forbid_receipt_update() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'replay must not rewrite settled receipts'; END;
$$;
CREATE TRIGGER product_replay_receipt_unchanged BEFORE UPDATE ON product_control_receipts
 FOR EACH ROW EXECUTE FUNCTION product_replay_forbid_receipt_update();
`)
	if err != nil {
		t.Fatalf("seed retained success and unsubscribed delivery: %v", err)
	}
}

func productControlReplayRows(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var rows string
	err := pool.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT to_jsonb(r) FROM product_control_receipts r),
 (SELECT to_jsonb(s) FROM product_control_email_subscriptions s),
 (SELECT to_jsonb(d) FROM product_control_email_deliveries d))::text`).Scan(&rows)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
