package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 408
func TestObjectLifecycleDeletionBindingMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);
 CREATE TABLE object_buckets(id uuid PRIMARY KEY,account_id uuid,state text);
 CREATE TABLE object_deletions(id uuid PRIMARY KEY,bucket_id uuid,object_key text,selector text,state text,target_provider_version_id text DEFAULT '');
 INSERT INTO accounts VALUES('00000000-0000-4000-8000-000000000001');
 INSERT INTO object_buckets VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','ready');
 INSERT INTO object_deletions VALUES('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000002','legacy','','completed','');`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261002233035408_object_lifecycle_scans.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[0]); err != nil {
		t.Fatal(err)
	}
	raw, err = migrations.FS.ReadFile("20261003001442856_object_lifecycle_deletion_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var legacy bool
	if err = pool.QueryRow(ctx, `SELECT lifecycle_scan_id IS NULL AND lifecycle_binding='{}' FROM object_deletions`).Scan(&legacy); err != nil || !legacy {
		t.Fatal("legacy receipt changed", legacy, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_bucket_lifecycle VALUES('00000000-0000-4000-8000-000000000002',1,'[{"id":"expire","status":"Enabled","filter":{"prefix":"logs/"},"expiration":{"days":1}}]',now(),now());
 INSERT INTO object_lifecycle_scans(id,bucket_id,revision,rules,retry_at,created_at,updated_at,lease_token,lease_until)
 SELECT '00000000-0000-4000-8000-000000000004',bucket_id,revision,rules,now(),now(),now(),'scan',now()+interval '2 minutes' FROM object_bucket_lifecycle;`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO object_deletions(id,bucket_id,object_key,selector,state,lifecycle_scan_id,lifecycle_binding) VALUES('00000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000002','logs/a','','prepared','00000000-0000-4000-8000-000000000004',jsonb_build_object('scan_id','00000000-0000-4000-8000-000000000004','scan_token','scan','rule_id','expire','kind','current','expected_provider_version_id','null','expected_last_modified',clock_timestamp()-interval '5 days'))`
	for _, bad := range []string{
		strings.Replace(insert, "'scan_token','scan'", "'scan_token','wrong'", 1),
		strings.Replace(insert, "'rule_id','expire'", "'rule_id','other'", 1),
		strings.Replace(insert, "'logs/a'", "'other/a'", 1),
		strings.Replace(insert, "'kind','current'", "'kind','replication'", 1),
		strings.Replace(insert, "clock_timestamp()-interval '5 days'", "clock_timestamp()+interval '5 days'", 1),
	} {
		if _, err = pool.Exec(ctx, bad); err == nil {
			t.Fatal("invalid lifecycle binding inserted", bad)
		}
	}
	if _, err = pool.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_deletions SET lifecycle_binding=jsonb_set(lifecycle_binding,'{rule_id}','"changed"') WHERE lifecycle_scan_id IS NOT NULL`); err == nil {
		t.Fatal("binding rewritten")
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback discarded bound receipt")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET lease_until=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_deletions SET state='dispatched' WHERE lifecycle_scan_id IS NOT NULL`); err == nil {
		t.Fatal("expired scan dispatched")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET lease_until=clock_timestamp()+interval '2 minutes';UPDATE object_deletions SET state='dispatched' WHERE lifecycle_scan_id IS NOT NULL`); err != nil {
		t.Fatal("live dispatch", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET lease_until=clock_timestamp()-interval '1 second';UPDATE object_deletions SET state='completed' WHERE lifecycle_scan_id IS NOT NULL`); err != nil {
		t.Fatal("dispatched action could not settle after scan expiry", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_lifecycle_scans`); err == nil {
		t.Fatal("scan removal orphaned bound receipt")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_deletions WHERE lifecycle_scan_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("empty binding rollback", err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("binding reapply", err)
	}
}
