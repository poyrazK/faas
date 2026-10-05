package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 550
func TestObjectLifecycleMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE object_buckets(id uuid PRIMARY KEY); INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001')`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261004090600373_object_lifecycle_scans.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_bucket_lifecycle VALUES('00000000-0000-0000-0000-000000000001',1,'[{"id":"expire","status":"Enabled","filter":{},"expiration":{"days":1}}]',now(),now());
 INSERT INTO object_lifecycle_scans(id,bucket_id,revision,rules,retry_at,created_at,updated_at) SELECT '00000000-0000-0000-0000-000000000002',bucket_id,revision,rules,now(),now(),now() FROM object_bucket_lifecycle;`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE object_lifecycle_scans SET revision=2`,
		`UPDATE object_lifecycle_scans SET rules='[]'`,
		`UPDATE object_lifecycle_scans SET created_at=created_at-interval '1 second'`,
		`UPDATE object_lifecycle_scans SET scanned_keys=2`,
		`UPDATE object_lifecycle_scans SET last_key='a'`,
		`UPDATE object_lifecycle_scans SET scanned_keys=1`,
		`UPDATE object_lifecycle_scans SET state='completed'`,
		`UPDATE object_lifecycle_scans SET lease_token='token'`,
		`UPDATE object_bucket_lifecycle SET revision=3`,
		`UPDATE object_bucket_lifecycle SET rules='[]'`,
	} {
		if _, err = pool.Exec(ctx, sql); err == nil {
			t.Fatal("accepted protected rewrite", sql)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET last_key='b',scanned_keys=1,lease_token='lease',lease_until=clock_timestamp()+interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET last_key='a',scanned_keys=2`); err == nil {
		t.Fatal("backward cursor")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_lifecycle SET revision=2,rules='[]'`); err == nil {
		t.Fatal("live scan replaced")
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback lost active policy")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET state='completed',lease_token='',lease_until=NULL,finished_at=clock_timestamp(),updated_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET retry_at=retry_at+interval '1 second'`); err == nil {
		t.Fatal("terminal history rewritten")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_bucket_lifecycle`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback lost scan history")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_buckets`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("reapply migration", err)
	}
}
