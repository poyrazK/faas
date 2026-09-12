//go:build !no_pg

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/pressly/goose/v3"
)

const (
	publicStatusPreviousMigrationVersion int64 = 20260912130000001
	publicStatusMigrationVersion         int64 = 20260912144727207
)

func TestMigrationPublicStatusBackfillConstraintsReplayAndRollback(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpTo(t, ctx, pool, publicStatusPreviousMigrationVersion)
	longTitle := strings.Repeat("x", 200)
	if _, err := pool.Exec(ctx, `insert into status_incidents(component,severity,message) values ('faas-control-plane','full_outage',$1)`, longTitle); err != nil {
		t.Fatalf("seed legacy incident: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into status_incidents(component,severity,message,resolved_at) values ('builderd','partial_outage','Legacy resolved incident',now())`); err != nil {
		t.Fatalf("seed resolved legacy incident: %v", err)
	}
	migrateUpTo(t, ctx, pool, publicStatusMigrationVersion)

	var publicID, kind, title, state string
	var components []string
	if err := pool.QueryRow(ctx, `select public_id::text,kind,title,lifecycle_state,affected_components from status_incidents where message=$1`, longTitle).Scan(&publicID, &kind, &title, &state, &components); err != nil {
		t.Fatalf("read backfill: %v", err)
	}
	if publicID == "" || kind != "incident" || len([]rune(title)) != 160 || state != "investigating" || len(components) != 5 {
		t.Fatalf("backfill = id:%q kind:%q title:%q state:%q components:%v", publicID, kind, title, state, components)
	}
	var updates int
	if err := pool.QueryRow(ctx, `select count(*) from status_incident_updates where incident_id=(select id from status_incidents where public_id=$1)`, publicID).Scan(&updates); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Fatalf("backfilled update count = %d, want 1", updates)
	}
	var resolvedUpdates int
	if err := pool.QueryRow(ctx, `select count(*) from status_incident_updates where incident_id=(select id from status_incidents where message='Legacy resolved incident')`).Scan(&resolvedUpdates); err != nil {
		t.Fatal(err)
	}
	if resolvedUpdates != 2 {
		t.Fatalf("resolved legacy update count = %d, want investigating and resolved", resolvedUpdates)
	}
	var rollingPublicID, rollingKind, rollingState string
	var rollingUpdates int
	if err := pool.QueryRow(ctx, `
		insert into status_incidents(component,severity,message)
		values ('apid','degraded','Rolling writer incident')
		returning public_id::text,kind,lifecycle_state`).Scan(&rollingPublicID, &rollingKind, &rollingState); err != nil {
		t.Fatalf("legacy writer insert after migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from status_incident_updates where incident_id=(select id from status_incidents where public_id=$1)`, rollingPublicID).Scan(&rollingUpdates); err != nil {
		t.Fatalf("legacy writer initial update: %v", err)
	}
	if rollingPublicID == "" || rollingKind != "incident" || rollingState != "investigating" || rollingUpdates != 1 {
		t.Fatalf("rolling writer defaults = id:%q kind:%q state:%q updates:%d", rollingPublicID, rollingKind, rollingState, rollingUpdates)
	}
	if _, err := pool.Exec(ctx, `update status_incidents set resolved_at=now() where public_id=$1`, rollingPublicID); err != nil {
		t.Fatalf("legacy writer resolve after migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `select lifecycle_state,(select count(*) from status_incident_updates where incident_id=status_incidents.id) from status_incidents where public_id=$1`, rollingPublicID).Scan(&rollingState, &rollingUpdates); err != nil {
		t.Fatal(err)
	}
	if rollingState != "resolved" || rollingUpdates != 2 {
		t.Fatalf("rolling writer resolve = state:%q updates:%d, want resolved with two updates", rollingState, rollingUpdates)
	}
	if _, err := pool.Exec(ctx, `update status_incident_updates set message='rewritten' where incident_id=(select id from status_incidents where public_id=$1)`, publicID); err == nil {
		t.Fatal("append-only update row accepted UPDATE")
	}

	_, err := pool.Exec(ctx, `insert into status_incidents(component,severity,message,public_id,kind,title,impact,affected_components,lifecycle_state,starts_at,updated_at,create_idempotency_key,created_by) values ('apid','degraded','bad',gen_random_uuid(),'incident','Bad component','degraded',array['database'],'investigating',now(),now(),'bad-component','operator')`)
	if err == nil {
		t.Fatal("invalid public component was accepted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("invalid component error = %v, want SQLSTATE 23514", err)
	}

	_, err = pool.Exec(ctx, `insert into status_incidents(component,severity,message,public_id,kind,title,impact,affected_components,lifecycle_state,starts_at,updated_at,create_idempotency_key,created_by) values ('apid','degraded','bad',gen_random_uuid(),'incident','Resolved without timestamp','degraded',array['api_console'],'resolved',now(),now(),'bad-terminal','operator')`)
	if err == nil {
		t.Fatal("terminal event without resolved_at was accepted")
	}

	migrateUpTo(t, ctx, pool, publicStatusMigrationVersion)
	migrateDownPublicStatus(t, ctx, pool)

	var columns int
	if err := pool.QueryRow(ctx, `select count(*) from information_schema.columns where table_schema=current_schema() and table_name='status_incidents' and column_name='public_id'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("public_id remained after exact migration rollback")
	}
}

// migrateDownPublicStatus rolls back the exact prefix staged by migrateUpTo.
// Later migrations in the repository are deliberately never applied, so this
// remains a v2 round-trip test even when newer migrations land on main.
func migrateDownPublicStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		t.Fatal("migrateDownPublicStatus: pool has no config")
	}
	sqlDB, err := sql.Open("pgx", stdlib.RegisterConnConfig(cfg.ConnConfig))
	if err != nil {
		t.Fatalf("migrateDownPublicStatus: open stdlib: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("migrateDownPublicStatus: set goose dialect: %v", err)
	}
	if err := goose.DownContext(ctx, sqlDB, "."); err != nil {
		t.Fatalf("migrateDownPublicStatus: %v", err)
	}
	var got int64
	if err := pool.QueryRow(ctx, `select coalesce(max(version_id), 0) from goose_db_version where is_applied`).Scan(&got); err != nil {
		t.Fatalf("migrateDownPublicStatus: read ledger: %v", err)
	}
	if got != publicStatusPreviousMigrationVersion {
		t.Fatalf("migration ledger at %d after rollback, want %d", got, publicStatusPreviousMigrationVersion)
	}
}
