package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 408
func TestObjectMultipartSignedPartFenceMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,part_count int NOT NULL,state text NOT NULL,created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL);
 INSERT INTO object_storage_multipart_uploads VALUES
 ('00000000-0000-0000-0000-000000000001',1,'active',now()-interval '1 hour',now()+interval '1 hour'),
 ('00000000-0000-0000-0000-000000000002',1,'aborting',now()-interval '2 hours',now()-interval '1 hour'),
 ('00000000-0000-0000-0000-000000000003',1,'completed',now()-interval '2 hours',now()-interval '1 hour'),
 ('00000000-0000-0000-0000-000000000004',0,'aborting',now()-interval '2 hours',now()-interval '1 hour');`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261003084627002_object_multipart_signed_part_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var safe bool
	if err = pool.QueryRow(ctx, `SELECT bool_and(CASE WHEN part_count=0 OR state='completed' THEN part_url_unsafe_until IS NULL ELSE part_url_unsafe_until>=greatest(expires_at,now())+interval '49 minutes' END) FROM object_storage_multipart_uploads`).Scan(&safe); err != nil || !safe {
		t.Fatal("legacy upgrade lost outstanding URL authority", safe, err)
	}
	for _, statement := range []string{
		`UPDATE object_storage_multipart_uploads SET part_url_unsafe_until=NULL WHERE id='00000000-0000-0000-0000-000000000001'`,
		`UPDATE object_storage_multipart_uploads SET part_url_unsafe_until=part_url_unsafe_until-interval '1 second' WHERE id='00000000-0000-0000-0000-000000000001'`,
		`UPDATE object_storage_multipart_uploads SET part_url_unsafe_until=part_url_unsafe_until+interval '1 second' WHERE id='00000000-0000-0000-0000-000000000002'`,
		`UPDATE object_storage_multipart_uploads SET part_url_unsafe_until=clock_timestamp() WHERE id='00000000-0000-0000-0000-000000000004'`,
	} {
		if _, err = pool.Exec(ctx, statement); err == nil {
			t.Fatal("unsafe deadline rewrite", statement)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET part_url_unsafe_until=part_url_unsafe_until+interval '1 minute' WHERE id='00000000-0000-0000-0000-000000000001'; UPDATE object_storage_multipart_uploads SET state='aborting' WHERE id='00000000-0000-0000-0000-000000000001';`); err != nil {
		t.Fatal("valid active URL or abort cutoff", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET part_url_unsafe_until=part_url_unsafe_until+interval '1 minute' WHERE id='00000000-0000-0000-0000-000000000001'`); err == nil {
		t.Fatal("post-cutoff URL published")
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded cleanup fences")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_storage_multipart_uploads`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("cold reapply", err)
	}
}
