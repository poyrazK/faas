package db_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const recoveryAuditVersion int64 = 20261002015945515

func recoveryPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("ledger recovery acceptance needs an explicit DATABASE_URL")
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("ledger recovery acceptance needs PostgreSQL 16 pg_dump")
	}
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	pool := pgtest.OpenMigrated(t)
	var version int
	if err := pool.QueryRow(context.Background(), "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version/10000 != 16 {
		t.Skip("ledger recovery is explicitly limited to PostgreSQL 16")
	}
	return pool
}

func recoveryExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func recoveryVersions(t *testing.T) []int64 {
	t.Helper()
	sources, err := migrations.ApplicationStandardRecoverySources()
	if err != nil {
		t.Fatal(err)
	}
	versions := make([]int64, 0, len(sources))
	for version := range sources {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

func recoveryForget(t *testing.T, pool *pgxpool.Pool) []int64 {
	t.Helper()
	versions := recoveryVersions(t)
	recoveryExec(t, pool, "DELETE FROM goose_db_version WHERE version_id=ANY($1)", versions)
	return versions
}

func recoverySeedApps(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var account, org, app string
	ctx := context.Background()
	if err := pool.QueryRow(ctx, "INSERT INTO accounts(email,plan) VALUES('ledger-test@example.com','pro') RETURNING id::text").Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO orgs(slug,name,personal_org,personal_owner_account_id,plan) VALUES('ledger-test','Ledger test',true,$1,'pro') RETURNING id::text", account).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO apps(account_id,org_id,slug,ram_mb) VALUES($1,$2,'owned',128) RETURNING id::text", account, org).Scan(&app); err != nil {
		t.Fatal(err)
	}
	// Historical account-only apps intentionally have no standards enrollment.
	recoveryExec(t, pool, "INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'legacy',128)", account)
	return app
}

func recoveryCustomerState(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var state string
	err := pool.QueryRow(context.Background(), `SELECT jsonb_build_object(
	 'apps',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM apps a),
	 'enrollments',(SELECT jsonb_agg(to_jsonb(e) ORDER BY app_id) FROM app_application_standards e),
	 'operations',(SELECT count(*) FROM application_standard_operations),
	 'targets',(SELECT count(*) FROM application_standard_operation_targets),
	 'admissions',(SELECT count(*) FROM instance_application_standard_admissions),
	 'boots',(SELECT count(*) FROM instance_application_standard_boots),
	 'promotions',(SELECT count(*) FROM instance_application_standard_promotions))::text`).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func recoveryAssertStillMissing(t *testing.T, pool *pgxpool.Pool, versions []int64) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM goose_db_version WHERE version_id=ANY($1)", versions).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("refused recovery appended %d ledger entries", count)
	}
}

func TestApplicationStandardLedgerRecoveryReviewedRepair(t *testing.T) {
	pool := recoveryPool(t)
	recoverySeedApps(t, pool)
	before := recoveryCustomerState(t, pool)
	versions := recoveryForget(t, pool)
	ctx := context.Background()
	var drift *db.SchemaDriftError
	if err := db.MigrateUp(ctx, pool); !errors.As(err, &drift) {
		t.Fatalf("ordinary migration must retain frozen replay refusal, got %v", err)
	}
	plan, err := db.PreviewApplicationStandardLedgerRecovery(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Repair) != len(versions) || len(plan.Remaining) != 0 || plan.ApplicationCount != 2 {
		t.Fatal("review does not describe the exact full standards gap")
	}
	recoveryAssertStillMissing(t, pool, versions)
	start := time.Now().UTC()
	receipt, err := db.ApplyApplicationStandardLedgerRecovery(ctx, pool, plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt.RepairedVersions, versions) || receipt.RecoveredAt.Before(start) || receipt.Actor == "" {
		t.Fatal("receipt lacks exact versions, actor or current recovery clock")
	}
	recoveryAssertFreshEvents(t, pool, versions, start)
	retry, err := db.ApplyApplicationStandardLedgerRecovery(ctx, pool, plan.ApprovalHash)
	if err != nil || !reflect.DeepEqual(receipt, retry) {
		t.Fatalf("exact receipt retry changed result: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if before != recoveryCustomerState(t, pool) {
		t.Fatal("ledger recovery changed customer settings or runtime history")
	}
	for _, sql := range []string{"UPDATE application_standard_ledger_recoveries SET actor='other'", "DELETE FROM application_standard_ledger_recoveries"} {
		_, err := pool.Exec(ctx, sql)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "application_standard_ledger_recovery_immutable" {
			t.Fatalf("receipt mutation was not refused: %v", err)
		}
	}
}

func recoveryAssertFreshEvents(t *testing.T, pool *pgxpool.Pool, versions []int64, start time.Time) {
	t.Helper()
	var count int
	var first time.Time
	if err := pool.QueryRow(context.Background(), "SELECT count(*),min(tstamp) FROM goose_db_version WHERE version_id=ANY($1) AND is_applied", versions).Scan(&count, &first); err != nil {
		t.Fatal(err)
	}
	if count != len(versions) || first.Before(start) {
		t.Fatal("recovery must append exactly one current event per missing version")
	}
}

func TestApplicationStandardLedgerRecoveryRefusesUnverifiedState(t *testing.T) {
	cases := []struct {
		name, sql string
		want      error
	}{
		{"extra column", "ALTER TABLE application_standard_versions ADD COLUMN unreviewed text", db.ErrMigrationRecoverySchema},
		{"nullable definition", "ALTER TABLE application_standard_versions ALTER COLUMN definition DROP NOT NULL", db.ErrMigrationRecoverySchema},
		{"missing immutability trigger", "DROP TRIGGER application_standard_version_immutable ON application_standard_versions", db.ErrMigrationRecoverySchema},
		{"different function", "CREATE OR REPLACE FUNCTION application_standard_ledger_recovery_immutable() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END;'", db.ErrMigrationRecoverySchema},
		{"extra writer", "GRANT INSERT ON application_standards TO PUBLIC", db.ErrMigrationRecoverySchema},
		{"global default writer", "ALTER DEFAULT PRIVILEGES GRANT INSERT ON TABLES TO PUBLIC", db.ErrMigrationRecoverySchema},
		{"different function owner", "ALTER FUNCTION application_standard_ledger_recovery_immutable() OWNER TO pg_database_owner", db.ErrMigrationRecoverySchema},
		{"unknown history", "INSERT INTO goose_db_version(version_id,is_applied) VALUES(99999999999999999,true)", db.ErrMigrationRecoveryHistory},
		{"legacy gap", "DELETE FROM goose_db_version WHERE version_id=1", db.ErrMigrationRecoveryHistory},
		{"rolled back history", "INSERT INTO goose_db_version(version_id,is_applied) VALUES(20260930170711001,false)", db.ErrMigrationRecoveryHistory},
		{"missing enrollment", "DELETE FROM app_application_standards", db.ErrMigrationRecoveryBackfill},
		{"missing captured original", "UPDATE app_application_standards SET base_settings=base_settings-'require_signed'", db.ErrMigrationRecoveryBackfill},
		{"missing materialized context", `UPDATE app_application_standards SET effective='{"sources":{"require_signed":[{}]}}'::jsonb`, db.ErrMigrationRecoveryBackfill},
		{"missing native history", `UPDATE compute_nodes SET vmmd_incarnation=gen_random_uuid();
ALTER TABLE application_standard_native_incarnations DISABLE TRIGGER USER;
DELETE FROM application_standard_native_incarnations;
ALTER TABLE application_standard_native_incarnations ENABLE TRIGGER USER;`, db.ErrMigrationRecoveryBackfill},
		{"missing logging session", `INSERT INTO application_standard_log_consumers(node_id,session_id,generation,registered_at)
SELECT id,gen_random_uuid(),1,clock_timestamp() FROM compute_nodes WHERE active AND role IS DISTINCT FROM 'control-plane';
ALTER TABLE application_standard_log_consumer_sessions DISABLE TRIGGER USER;
DELETE FROM application_standard_log_consumer_sessions;
ALTER TABLE application_standard_log_consumer_sessions ENABLE TRIGGER USER;`, db.ErrMigrationRecoveryBackfill},
		{"unfenced unmatched snapshot", `DO $$ DECLARE a apps; d uuid; BEGIN
SELECT * INTO a FROM apps WHERE org_id IS NOT NULL LIMIT 1;
INSERT INTO deployments(app_id,kind,image_digest,status) VALUES(a.id,'image','sha256:recovery','pending') RETURNING id INTO d;
ALTER TABLE application_standard_snapshot_captures DISABLE TRIGGER USER;
ALTER TABLE snapshots DISABLE TRIGGER USER;
INSERT INTO application_standard_snapshot_captures(token,instance_id,app_id,deployment_id,account_id,node_id,parent_token,memory_key,expected_state,grant_data,input_snapshot)
VALUES(gen_random_uuid(),gen_random_uuid(),a.id,d,a.account_id,gen_random_uuid(),gen_random_uuid(),'recovery/unmatched.mem','running','{}','{}');
INSERT INTO snapshots(deployment_id,fc_version,mem_bytes,disk_bytes,storage_key) VALUES(d,'fixture',1,1,'recovery/unmatched.mem');
ALTER TABLE snapshots ENABLE TRIGGER USER;
ALTER TABLE application_standard_snapshot_captures ENABLE TRIGGER USER;
END $$;`, db.ErrMigrationRecoveryBackfill},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := recoveryPool(t)
			recoverySeedApps(t, pool)
			versions := recoveryForget(t, pool)
			recoveryExec(t, pool, tc.sql)
			if _, err := db.PreviewApplicationStandardLedgerRecovery(context.Background(), pool); !errors.Is(err, tc.want) {
				t.Fatalf("preview got %v, want %v", err, tc.want)
			}
			recoveryAssertStillMissing(t, pool, versionsExceptRollback(versions, tc.name))
		})
	}
}

func versionsExceptRollback(versions []int64, name string) []int64 {
	if name == "rolled back history" {
		return versions[1:]
	}
	return versions
}

func TestApplicationStandardLedgerRecoveryStaleApproval(t *testing.T) {
	pool := recoveryPool(t)
	versions := recoveryForget(t, pool)
	ctx := context.Background()
	plan, err := db.PreviewApplicationStandardLedgerRecovery(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"", "invalid", strings.ToUpper(plan.ApprovalHash), strings.Repeat("0", 64)} {
		if _, err := db.ApplyApplicationStandardLedgerRecovery(ctx, pool, hash); !errors.Is(err, db.ErrMigrationRecoveryStale) {
			t.Fatalf("invalid or unapproved plan accepted: %v", err)
		}
	}
	recoverySeedApps(t, pool)
	if _, err := db.ApplyApplicationStandardLedgerRecovery(ctx, pool, plan.ApprovalHash); !errors.Is(err, db.ErrMigrationRecoveryStale) {
		t.Fatalf("changed target membership accepted: %v", err)
	}
	recoveryAssertStillMissing(t, pool, versions)
}

func TestApplicationStandardLedgerRecoveryRejectsCopiedPlan(t *testing.T) {
	first, second := recoveryPool(t), recoveryPool(t)
	recoveryForget(t, first)
	versions := recoveryForget(t, second)
	rows, err := migrationsqlc.New().ReadMigrationRecoveryLedger(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	values := make([][]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, []any{row.ID, row.VersionID, row.IsApplied, row.Tstamp})
	}
	recoveryExec(t, second, "TRUNCATE goose_db_version")
	if _, err := second.CopyFrom(context.Background(), pgx.Identifier{"goose_db_version"}, []string{"id", "version_id", "is_applied", "tstamp"}, pgx.CopyFromRows(values)); err != nil {
		t.Fatal(err)
	}
	plan, err := db.PreviewApplicationStandardLedgerRecovery(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.PreviewApplicationStandardLedgerRecovery(context.Background(), second)
	if err != nil || plan.LedgerHash != other.LedgerHash || plan.SchemaHash != other.SchemaHash || plan.TargetHash == other.TargetHash {
		t.Fatalf("copied-ledger fixture did not isolate database identity: %v", err)
	}
	if _, err := db.ApplyApplicationStandardLedgerRecovery(context.Background(), second, plan.ApprovalHash); !errors.Is(err, db.ErrMigrationRecoveryStale) {
		t.Fatalf("another database accepted copied approval: %v", err)
	}
	recoveryAssertStillMissing(t, second, versions)
}

func TestApplicationStandardLedgerRecoveryPrepareFrozenAudit(t *testing.T) {
	pool := recoveryPool(t)
	versions := recoveryForget(t, pool)
	recoveryExec(t, pool, "DROP TABLE application_standard_ledger_recoveries; DROP FUNCTION application_standard_ledger_recovery_immutable()")
	recoveryExec(t, pool, "DELETE FROM goose_db_version WHERE version_id=$1", recoveryAuditVersion)
	ctx := context.Background()
	if err := db.PrepareApplicationStandardLedgerRecovery(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareApplicationStandardLedgerRecovery(ctx, pool); err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	recoveryAssertStillMissing(t, pool, versions)
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=$1", recoveryAuditVersion).Scan(&count); err != nil || count != 1 {
		t.Fatalf("prepare did not apply only one actual audit migration: count=%d err=%v", count, err)
	}
	plan, err := db.PreviewApplicationStandardLedgerRecovery(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyApplicationStandardLedgerRecovery(ctx, pool, plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationStandardLedgerRecoveryCanceledReceiptRollsBackEvents(t *testing.T) {
	pool := recoveryPool(t)
	versions := recoveryForget(t, pool)
	ctx := context.Background()
	plan, err := db.PreviewApplicationStandardLedgerRecovery(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conflict.Rollback(ctx)
	var pid int
	if err := conflict.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Uncommitted test-only collision: invisible to review, but blocks the final
	// receipt insert after the repair's ledger events have been inserted.
	_, err = conflict.Exec(ctx, `INSERT INTO application_standard_ledger_recoveries
	 (approval_hash,target_hash,schema_hash,source_hash,ledger_hash,plan,repaired_versions)
	 VALUES($1,$2,$3,$4,$5,'{}',$6)`, plan.ApprovalHash, plan.TargetHash, plan.SchemaHash, plan.SourceHash, plan.LedgerHash, versions)
	if err != nil {
		t.Fatal(err)
	}
	applyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := db.ApplyApplicationStandardLedgerRecovery(applyCtx, pool, plan.ApprovalHash)
		result <- err
	}()
	recoveryWaitBlocked(t, pool, pid)
	cancel()
	if err := <-result; err == nil {
		t.Fatal("canceled receipt insert succeeded")
	}
	if err := conflict.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	recoveryAssertStillMissing(t, pool, versions)
	retryCtx, retryCancel := context.WithTimeout(ctx, 5*time.Second)
	defer retryCancel()
	if _, err := db.ApplyApplicationStandardLedgerRecovery(retryCtx, pool, plan.ApprovalHash); err != nil {
		t.Fatalf("retry after transaction rollback: %v", err)
	}
}

func recoveryWaitBlocked(t *testing.T, pool *pgxpool.Pool, blocker int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
		 WHERE datname=current_database() AND $1::integer=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("did not observe the final receipt insert blocked by its conflicting transaction")
}
