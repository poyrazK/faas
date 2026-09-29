// adr: 369 — egress flow log retention.
package meter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRetentionOnceEgressFlowLogBatches(t *testing.T) {
	r := &recordingExecer{rowsFn: func(i int) int64 {
		if i == 0 {
			return RetentionBatchSize
		}
		return 3
	}}
	n, err := RetentionOnceEgressFlowLog(context.Background(), r)
	if err != nil || n != RetentionBatchSize+3 {
		t.Fatalf("RetentionOnceEgressFlowLog = %d, %v", n, err)
	}
	calls := r.callsCopy()
	if len(calls) != 2 || !strings.Contains(calls[0].SQL, "egress_flow_log") || calls[0].Args[0] != "30 days" {
		t.Fatalf("calls = %+v, want two bounded deletes over 30 days", calls)
	}
}

type pgRetentionExecer struct{ pool *pgxpool.Pool }

func (p pgRetentionExecer) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := p.pool.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

// The retention SQL runs against the real table and keeps rows inside the
// window.
func TestRetentionOnceEgressFlowLogPostgres(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM egress_flow_log`); err != nil {
		t.Fatal(err)
	}
	for _, age := range []time.Duration{31 * 24 * time.Hour, 29 * 24 * time.Hour} {
		if _, err := pool.Exec(ctx, `INSERT INTO egress_flow_log (observed_at, node_name, instance_id, remote_ip, remote_port)
			VALUES ($1, 'fsn-2', 'i-1', '198.51.100.10', 443)`, time.Now().Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := RetentionOnceEgressFlowLog(ctx, pgRetentionExecer{pool}); err != nil || n != 1 {
		t.Fatalf("RetentionOnceEgressFlowLog = %d, %v; want the 31-day row deleted", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM egress_flow_log`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("rows left = %d, %v; want 1", left, err)
	}
}
