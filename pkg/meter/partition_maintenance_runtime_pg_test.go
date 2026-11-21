//go:build !no_pg

package meter

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// runtimeRoleDB runs every statement as a role that holds row DML and
// EXECUTE on the maintenance functions but owns nothing, as meterd's login
// does on prod (ADR-20261023104734).
type runtimeRoleDB struct {
	pool *pgxpool.Pool
	role string
}

func (d runtimeRoleDB) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{d.role}.Sanitize()); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

func (d runtimeRoleDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

// TestPartitionMaintenanceRunsWithoutDDL pins the 2026-11-17 prod failure:
// meterd's login could not drop request_telemetry_202610 ("must be owner"),
// and would not have been able to create the next month's partitions either.
// Through the owner-run functions a role with no DDL creates, attaches and
// drops partitions of both tables; a role without EXECUTE cannot call them.
func TestPartitionMaintenanceRunsWithoutDDL(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	var schema string
	if err := pool.QueryRow(ctx, `select current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	runtime, outsider := "meterd_rt_"+suffix, "outsider_rt_"+suffix
	for _, role := range []string{runtime, outsider} {
		ident := pgx.Identifier{role}.Sanitize()
		if _, err := pool.Exec(ctx, "CREATE ROLE "+ident+" NOLOGIN"); err != nil {
			t.Skipf("test cluster cannot create roles: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+ident)
			_, _ = pool.Exec(context.Background(), "DROP ROLE IF EXISTS "+ident)
		})
		for _, grant := range []string{
			fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", pgx.Identifier{schema}.Sanitize(), ident),
			fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s", pgx.Identifier{schema}.Sanitize(), ident),
		} {
			if _, err := pool.Exec(ctx, grant); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, fn := range []string{"monthly_partitions_ensure(regclass)", "monthly_partitions_drop_expired(regclass)"} {
		if _, err := pool.Exec(ctx, fmt.Sprintf("GRANT EXECUTE ON FUNCTION %s.%s TO %s", pgx.Identifier{schema}.Sanitize(), fn, pgx.Identifier{runtime}.Sanitize())); err != nil {
			t.Fatal(err)
		}
	}

	// Start from the shape prod was in: this month's partitions missing and
	// an expired one still attached, for both tables.
	for _, table := range []string{"request_telemetry", "log_events"} {
		var current string
		if err := pool.QueryRow(ctx, `select $1 || '_' || to_char(date_trunc('month', now() AT TIME ZONE 'UTC'), 'YYYYMM')`, table).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{schema, current}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		expired := table + "_202001"
		if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s FOR VALUES FROM ('2020-01-01 00:00:00+00') TO ('2020-02-01 00:00:00+00')`,
			pgx.Identifier{schema, expired}.Sanitize(), pgx.Identifier{schema, table}.Sanitize())); err != nil {
			t.Fatal(err)
		}
	}

	rt := runtimeRoleDB{pool: pool, role: runtime}
	// The old in-daemon DDL fails for this role, as it did on prod.
	if _, err := rt.Exec(ctx, "DROP TABLE "+pgx.Identifier{schema, "request_telemetry_202001"}.Sanitize()); err == nil || !strings.Contains(err.Error(), "must be owner") {
		t.Fatalf("runtime role dropped a partition directly: %v", err)
	}

	tc, err := EnsureRequestTelemetryPartitions(ctx, rt)
	if err != nil || tc.CoveredMonths != 3 || !tc.CurrentMonthCovered {
		t.Fatalf("request telemetry ensure as runtime role = %+v, %v", tc, err)
	}
	lc, err := EnsureLogEventPartitions(ctx, rt)
	if err != nil || lc.CoveredMonths != 3 || !lc.CurrentMonthCovered {
		t.Fatalf("log event ensure as runtime role = %+v, %v", lc, err)
	}
	if _, err := DropExpiredRequestTelemetryPartitions(ctx, rt); err != nil {
		t.Fatalf("request telemetry drop as runtime role: %v", err)
	}
	if err := DropExpiredLogEventPartitions(ctx, rt); err != nil {
		t.Fatalf("log event drop as runtime role: %v", err)
	}
	for _, expired := range []string{"request_telemetry_202001", "log_events_202001"} {
		var exists bool
		if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, schema+"."+expired).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("%s was not dropped", expired)
		}
	}

	out := runtimeRoleDB{pool: pool, role: outsider}
	if _, err := EnsureRequestTelemetryPartitions(ctx, out); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("a role without EXECUTE ran partition maintenance: %v", err)
	}
	// The functions touch only the two maintained tables.
	if _, err := rt.Exec(ctx, `SELECT monthly_partitions_drop_expired('apps'::regclass)`); err == nil {
		t.Fatal("drop_expired accepted a table it does not maintain")
	}
}
