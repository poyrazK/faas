//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const clientIPProvenanceCutoverVersion int64 = 20260915120000002

func TestMigrations_AnnotateLegacyLoopbackAuditIPs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	accountID := seedAccount(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		insert into events(actor, kind, subject, data) values
			('apid', 'auth.session.created', $1, '{"issued_ip":"127.0.0.1"}'::jsonb),
			('apid', 'key.created', $1, '{"created_ip":"127.0.0.1"}'::jsonb),
			('apid', 'auth.session.created', $1, '{"issued_ip":"203.0.113.8"}'::jsonb)`, accountID); err != nil {
		t.Fatalf("seed audit rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, clientIPProvenanceCutoverVersion); err != nil {
		t.Fatalf("rewind provenance migration: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply provenance migration: %v", err)
	}

	var count int
	var strategy, historical, migrationVersion string
	var hasCutoverAt bool
	if err := pool.QueryRow(ctx, `
		select count(*), max(data->>'strategy'), max(data->>'historical_client_ip'),
		       max(data->>'migration_version'), bool_or(data ? 'cutover_at')
		from events
		where kind = 'security.client_ip_provenance_cutover'
		  and subject = $1`, accountID).Scan(&count, &strategy, &historical, &migrationVersion, &hasCutoverAt); err != nil {
		t.Fatalf("read provenance marker: %v", err)
	}
	if count != 1 {
		t.Fatalf("provenance marker count = %d, want 1", count)
	}
	if strategy != "retain_source_rows_and_treat_legacy_loopback_as_unknown" || historical != "unrecoverable" {
		t.Fatalf("provenance marker = (%q, %q)", strategy, historical)
	}
	if migrationVersion != "20260915120000002" || hasCutoverAt {
		t.Fatalf("migration marker version=%q has_cutover_at=%t", migrationVersion, hasCutoverAt)
	}

	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, clientIPProvenanceCutoverVersion); err != nil {
		t.Fatalf("rewind provenance migration for replay: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay provenance migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from events
		where kind = 'security.client_ip_provenance_cutover'
		  and subject = $1`, accountID).Scan(&count); err != nil {
		t.Fatalf("read replayed provenance marker: %v", err)
	}
	if count != 1 {
		t.Fatalf("replayed provenance marker count = %d, want 1", count)
	}
}
