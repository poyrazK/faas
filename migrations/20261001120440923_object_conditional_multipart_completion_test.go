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

func TestObjectConditionalMultipartMigrationGuards(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261001120440923_object_conditional_multipart_completion.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	assertCode := func(err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("err=%v, want SQLSTATE %s", err, code)
		}
	}
	_, err = pool.Exec(ctx, `CREATE TABLE object_storage_multipart_uploads (
id uuid PRIMARY KEY, bucket_id uuid, object_key text, retry_at timestamptz, part_count integer NOT NULL,
state text NOT NULL CONSTRAINT object_storage_multipart_uploads_state_check CHECK (state IN ('initiating','active','completing','aborting','completed','aborted')));
CREATE UNIQUE INDEX object_storage_multipart_live_key_idx ON object_storage_multipart_uploads(bucket_id,object_key) WHERE state IN ('initiating','active','completing','aborting');
CREATE INDEX object_storage_multipart_retry_idx ON object_storage_multipart_uploads(retry_at,id) WHERE state IN ('initiating','completing','aborting');
INSERT INTO object_storage_multipart_uploads VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','key',now(),0,'active');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{
		`completion_if_none_match='*'`,
		`state='completing_conditional'`,
		`state='completing_conditional',completion_if_match='etag',completion_if_none_match='*'`,
		`state='completing_conditional',completion_if_match=repeat('a',257)`,
		`state='completing_conditional',completion_if_match=E'a\nb'`,
		`state='completing_conditional',completion_if_none_match='etag'`,
		`state='completing_conditional',completion_if_none_match='*',part_count=1`,
		`state='aborting',completion_error_code='provider-secret'`,
	} {
		_, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET `+set)
		assertCode(err, "23514")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='completing_conditional',completion_if_none_match='*'`); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{`completion_if_none_match=''`, `completion_if_none_match='',completion_if_match='other'`, `state='completing',completion_if_none_match=''`} {
		_, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET `+set)
		assertCode(err, "23514")
	}
	var oldWorkerCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_multipart_uploads WHERE state IN ('initiating','completing','aborting')`).Scan(&oldWorkerCount); err != nil || oldWorkerCount != 0 {
		t.Fatal("old recovery worker can see conditional completion", oldWorkerCount, err)
	}
	_, err = pool.Exec(ctx, sections[1])
	assertCode(err, "P0001")
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='aborting',completion_error_code='precondition_failed'`); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET completion_error_code=''`)
	assertCode(err, "23514")
	_, err = pool.Exec(ctx, sections[1])
	assertCode(err, "P0001")
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='aborted'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
}
