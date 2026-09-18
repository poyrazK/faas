//go:build !no_pg

// adr: 048

package meter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type rollupPGAdapter struct {
	pool *pgxpool.Pool
}

func (a rollupPGAdapter) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	// Production tables live in public. pgtest intentionally migrates an
	// isolated schema, so only strip those two fixed qualifiers while keeping
	// the SELECT/GROUP BY/ON CONFLICT statement byte-for-byte otherwise.
	query = strings.ReplaceAll(query, "public.usage_daily", "usage_daily")
	query = strings.ReplaceAll(query, "public.usage_minutes", "usage_minutes")
	tag, err := a.pool.Exec(ctx, query, args...)
	return tag.RowsAffected(), err
}

func TestRollupOnceExecutesAgainstPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	accountID := uuid.New()
	appID := uuid.New()
	start := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	for i, requests := range []int{2, 3} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			accountID, appID, uuid.New(), start.Add(time.Duration(i)*time.Minute), 60*(i+1), requests); err != nil {
			t.Fatalf("insert usage minute %d: %v", i, err)
		}
	}

	rows, err := RollupOnce(ctx, rollupPGAdapter{pool: pool}, start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("RollupOnce: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}

	var mbSeconds, requests int64
	if err := pool.QueryRow(ctx, `
		SELECT mb_seconds, requests
		  FROM usage_daily
		 WHERE account_id = $1 AND app_id = $2 AND day = $3`,
		accountID, appID, start).Scan(&mbSeconds, &requests); err != nil {
		t.Fatalf("read usage_daily: %v", err)
	}
	if mbSeconds != 180 || requests != 5 {
		t.Fatalf("usage_daily = mb_seconds:%d requests:%d, want 180 and 5", mbSeconds, requests)
	}
}
