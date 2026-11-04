//go:build !no_pg

// adr: 127

package meter

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// telemetryRetentionPG runs the production statements against pgtest's
// isolated schema, which has no public. qualifier.
type telemetryRetentionPG struct{ pool *pgxpool.Pool }

func (a telemetryRetentionPG) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := a.pool.Exec(ctx, strings.ReplaceAll(query, "public.", ""), args...)
	return tag.RowsAffected(), err
}

func (a telemetryRetentionPG) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return a.pool.QueryRow(ctx, strings.ReplaceAll(query, "public.", ""), args...)
}

// request_telemetry is partitioned by month and a ctid is unique only within
// a partition. The sweep deleted by ctid alone, so every expired row also took
// the rows at the same position in every other partition: here a fresh row
// in the current month shares the expired row's position in the default
// partition.
func TestRequestTelemetryRetentionKeepsOtherPartitionsRows(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := EnsureRequestTelemetryPartitions(ctx, telemetryRetentionPG{pool}); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO request_telemetry (account_id, app_id, deployment_id, route, method, status, latency_ms, received_at)
VALUES ($1, $2, $3, 'GET /', 'GET', 200, 5, `
	// An app with no resource account keeps one day; 2001 lands in the
	// default partition, now in the current month's.
	expiredApp, freshApp := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, insert+`'2001-01-01T00:00:00Z')`, uuid.New(), expiredApp, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, insert+`now())`, uuid.New(), freshApp, uuid.New()); err != nil {
		t.Fatal(err)
	}
	var sameSlot bool
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT ctid) = 1 AND count(DISTINCT tableoid) = 2 FROM request_telemetry WHERE app_id = ANY($1)`,
		[]uuid.UUID{expiredApp, freshApp}).Scan(&sameSlot); err != nil {
		t.Fatal(err)
	}
	if !sameSlot {
		t.Fatal("fixture rows must share a ctid in different partitions")
	}

	if _, err := RetentionOnceRequestTelemetry(ctx, telemetryRetentionPG{pool}); err != nil {
		t.Fatal(err)
	}
	var expired, fresh int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE app_id = $1), count(*) FILTER (WHERE app_id = $2) FROM request_telemetry`,
		expiredApp, freshApp).Scan(&expired, &fresh); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || fresh != 1 {
		t.Fatalf("after the sweep: expired rows %d (want 0), retained rows %d (want 1)", expired, fresh)
	}
}
