//go:build !no_pg

package migrations_test

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectS3MigrationReplayPreservesSessions(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Include a public session, an undispatched completion and fixed-size
	// sessions with issued URL deadlines. Replay must preserve their authority.
	if _, err := pool.Exec(ctx, `
 INSERT INTO accounts(id,email) VALUES('00000000-0000-0000-0000-000000000001','replay@example.com');
 INSERT INTO apps(id,account_id,slug,ram_mb) VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','replay',128);
 INSERT INTO object_buckets(id,account_id,app_id,name,scope,region,backend_id,backend_fingerprint,physical_name,state)
 VALUES('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','replay','app','local','test',repeat('a',64),'replay-physical','ready');
 INSERT INTO object_storage_multipart_uploads(id,account_id,app_id,bucket_id,object_key,size_bytes,part_size_bytes,part_count,provider_upload_id,state,expires_at,part_url_unsafe_until)
 SELECT gen_random_uuid(),'00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000003',key,bytes,bytes,parts,'provider',state,now()+interval '1 hour',CASE WHEN parts>0 THEN now()+interval '30 minutes' END
 FROM (VALUES ('public',0::bigint,0,'active'),('active',5::bigint,1,'active'),('completion',5::bigint,1,'completing'),('abort',5::bigint,1,'aborting')) AS session(key,bytes,parts,state);
 `); err != nil {
		t.Fatal(err)
	}
	snapshot := func(query string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	const sessionsQuery = `SELECT jsonb_agg(to_jsonb(u) ORDER BY id)::text FROM object_storage_multipart_uploads u`
	beforeSessions := snapshot(sessionsQuery)
	beforeSchema := snapshot(objectS3ReplaySchemaQuery)
	names, err := fs.Glob(migrations.FS, "202610040906*.sql")
	if err != nil || len(names) == 0 {
		t.Fatal("missing S3 migration set", err)
	}
	versions := make([]int64, 0, len(names))
	for _, name := range names {
		version, err := strconv.ParseInt(strings.SplitN(name, "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	if tag, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=ANY($1::bigint[])`, versions); err != nil || tag.RowsAffected() != int64(len(versions)) {
		t.Fatal("incomplete replay set", tag, err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal("replay with live multipart sessions", err)
	}
	if got := snapshot(sessionsQuery); got != beforeSessions {
		t.Fatal("replay changed multipart state or cleanup authority", got)
	}
	if got := snapshot(objectS3ReplaySchemaQuery); got != beforeSchema {
		t.Fatal("replay changed S3 constraints, indexes, triggers or functions")
	}
}

const objectS3ReplaySchemaQuery = `
 SELECT jsonb_agg(jsonb_build_array(kind,name,definition) ORDER BY kind,name,definition)::text
 FROM (
  SELECT 'constraint' AS kind,c.conrelid::regclass::text||'.'||c.conname AS name,pg_get_constraintdef(c.oid) AS definition
  FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
  WHERE n.nspname=current_schema() AND r.relname LIKE 'object_%'
  UNION ALL
  SELECT 'function',p.oid::regprocedure::text,pg_get_functiondef(p.oid)
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND p.proname LIKE '%object%'
  UNION ALL
  SELECT 'trigger',t.tgrelid::regclass::text||'.'||t.tgname,pg_get_triggerdef(t.oid)
  FROM pg_trigger t JOIN pg_class r ON r.oid=t.tgrelid JOIN pg_namespace n ON n.oid=r.relnamespace
  WHERE n.nspname=current_schema() AND r.relname LIKE 'object_%' AND NOT t.tgisinternal
  UNION ALL
  SELECT 'index',i.indexrelid::regclass::text,pg_get_indexdef(i.indexrelid)
  FROM pg_index i JOIN pg_class r ON r.oid=i.indrelid JOIN pg_namespace n ON n.oid=r.relnamespace
  WHERE n.nspname=current_schema() AND r.relname LIKE 'object_%'
 ) AS definitions`
