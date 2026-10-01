//go:build !no_pg

package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectMultipartTransferMigrationGuards(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261001105910830_object_multipart_transfer_cleanup.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	assertCode := func(err error, code string) {
		t.Helper()
		var e *pgconn.PgError
		if !errors.As(err, &e) || e.Code != code {
			t.Fatalf("err=%v, want SQLSTATE %s", err, code)
		}
	}
	_, err = pool.Exec(ctx, `CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,part_count integer NOT NULL,state text NOT NULL);
CREATE TABLE object_storage_multipart_part_grants(upload_id uuid,part_number integer,max_bytes bigint);
INSERT INTO object_storage_multipart_uploads VALUES ('00000000-0000-0000-0000-000000000001',0,'active');
INSERT INTO object_storage_multipart_part_grants VALUES ('00000000-0000-0000-0000-000000000001',1,30);`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, sections[0])
	assertCode(err, "P0001")
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='aborted'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var tracked bool
	if err = pool.QueryRow(ctx, `SELECT cleanup_tracked FROM object_storage_multipart_part_grants`).Scan(&tracked); err != nil || tracked {
		t.Fatalf("legacy grant became reclaimable: %v %v", tracked, err)
	}
	for _, set := range []string{"transfer_token='token'", "unsafe_until=now()", "transfer_token='',unsafe_until=now()", "transfer_token='token',unsafe_until='infinity'"} {
		_, err = pool.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET `+set)
		assertCode(err, "23514")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET transfer_token='token',unsafe_until=now()+interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, sections[1])
	assertCode(err, "P0001")
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET transfer_token=NULL,unsafe_until=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
}
