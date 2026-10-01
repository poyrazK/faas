//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectMultipartCapacityMigrationGuards(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	sql, err := migrations.FS.ReadFile("20261001100142202_object_multipart_capacity.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(sql), "-- +goose Down", 2)
	if len(sections) != 2 {
		t.Fatal("missing rollback")
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,part_count integer NOT NULL,state text NOT NULL);
 INSERT INTO object_storage_multipart_uploads VALUES ('00000000-0000-0000-0000-000000000001',0,'active');`); err != nil {
		t.Fatal(err)
	}
	assertCode := func(err error, code string) {
		t.Helper()
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != code {
			t.Fatalf("err=%v, want SQLSTATE %s", err, code)
		}
	}
	_, err = pool.Exec(ctx, sections[0])
	assertCode(err, "P0001")
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='aborted'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	for _, values := range []string{"0,30", "1,0", "10001,30", "1,5368709121"} {
		_, err = pool.Exec(ctx, `INSERT INTO object_storage_multipart_part_grants VALUES ('00000000-0000-0000-0000-000000000001',`+values+`)`)
		assertCode(err, "23514")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_multipart_part_grants VALUES ('00000000-0000-0000-0000-000000000001',1,30)`); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, sections[1])
	assertCode(err, "P0001")
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='completed'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
}
