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
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type partitionTestDB struct{ pool *pgxpool.Pool }

func (d partitionTestDB) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := d.pool.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

func (d partitionTestDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

func TestEnsureRequestTelemetryPartitionsMovesDefaultRowsAndIsIdempotent(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var currentName string
	if err := pool.QueryRow(ctx, `select 'request_telemetry_' || to_char(date_trunc('month', now()), 'YYYYMM')`).Scan(&currentName); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS public."+currentName); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into request_telemetry
		    (id, account_id, app_id, deployment_id, route, method, status, latency_ms, received_at)
		values ($1, $2, $3, $4, '/health', 'GET', 200, 3, now())
	`, id, uuid.NewString(), uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(ctx, `select tableoid::regclass::text from request_telemetry where id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != "request_telemetry_default" {
		t.Fatalf("initial partition=%q, want default", before)
	}

	partDB := partitionTestDB{pool: pool}
	for pass := 0; pass < 2; pass++ {
		coverage, err := EnsureRequestTelemetryPartitions(ctx, partDB)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if coverage.CoveredMonths != 3 || !coverage.CurrentMonthCovered || coverage.DefaultRows != 0 {
			t.Fatalf("pass %d coverage=%+v", pass, coverage)
		}
	}
	var after string
	if err := pool.QueryRow(ctx, `select tableoid::regclass::text from request_telemetry where id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != currentName {
		t.Fatalf("reconciled partition=%q, want %q", after, currentName)
	}
}

func TestDropExpiredPartitionsUsesLongestPlanWindow(t *testing.T) {
	if !containsAll(retentionDropExpiredPartitionsSQL,
		"partstart + interval '1 month'",
		fmt.Sprintf("interval '%d days'", DefaultRequestTelemetryRetentionDays),
	) {
		t.Fatalf("partition drop cutoff does not preserve the longest plan window:\n%s", retentionDropExpiredPartitionsSQL)
	}
}

func containsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}
