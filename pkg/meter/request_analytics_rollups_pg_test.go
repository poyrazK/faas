//go:build !no_pg

// adr: 20261103113952

package meter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollupRetentionPG runs the retention SQL against pgtest's per-test schema,
// which is not public.
type rollupRetentionPG struct{ pool *pgxpool.Pool }

func (p rollupRetentionPG) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := p.pool.Exec(ctx, strings.ReplaceAll(sql, "public.", ""), args...)
	return tag.RowsAffected(), err
}

func TestRequestAnalyticsRollupsAddUpAndAgeOutOnThePlanLadder(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollups@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollups"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	deployment := uuid.New()
	now := time.Now().UTC().Truncate(time.Minute)
	row := func(minute time.Time, requests int64, maxMS int32, bucket int) state.RequestAnalyticsRollup {
		r := state.RequestAnalyticsRollup{
			AppID: appID, AccountID: accountID, DeploymentID: deployment, BucketStart: minute,
			DimensionKind: "route", Dimension: "GET /items", Method: "GET", Status: 200,
			Requests: requests, LatencyMaxMS: maxMS,
		}
		r.LatencyHist[bucket] = requests
		return r
	}
	old := now.Add(-4 * 24 * time.Hour) // past Hobby's 3 days

	// Two flushes of the same key add up; the earlier coverage minute wins.
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{row(now, 3, 40, 3), row(old, 1, 5, 0)},
		[]state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: now}}); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{row(now, 2, 900, 24)},
		[]state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: old}, {AppID: uuid.New(), CoveredSince: now}}); err != nil {
		t.Fatalf("second flush (with a coverage row for an app that is gone): %v", err)
	}
	var requests, maxMS int64
	var hist []int64
	if err := pool.QueryRow(ctx, `SELECT requests, latency_max_ms, latency_hist FROM request_analytics_minutes WHERE app_id = $1 AND bucket_start = $2`,
		appID, now).Scan(&requests, &maxMS, &hist); err != nil {
		t.Fatal(err)
	}
	if requests != 5 || maxMS != 900 || len(hist) != api.RequestAnalyticsLatencyBuckets || hist[3] != 3 || hist[24] != 2 {
		t.Fatalf("merged row = requests %d max %d hist %v, want 5, 900, 3 at [3] and 2 at [24]", requests, maxMS, hist)
	}
	since, ok, err := store.RequestAnalyticsRollupCoverage(ctx, appID)
	if err != nil || !ok || !since.Equal(old) {
		t.Fatalf("coverage = %v %v %v, want %v", since, ok, err, old)
	}

	// A histogram of another width is refused, not truncated.
	if _, err := pool.Exec(ctx, `UPDATE request_analytics_minutes SET latency_hist = request_analytics_hist_add(latency_hist, ARRAY[1]::bigint[])`); err == nil {
		t.Fatal("a one-bucket histogram was added to a full-width one")
	}

	deleted, err := RetentionOnceRequestAnalyticsMinutes(ctx, rollupRetentionPG{pool})
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM request_analytics_minutes WHERE app_id = $1`, appID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if deleted != 1 || left != 1 {
		t.Fatalf("retention deleted %d, left %d; want the 4-day-old Hobby row gone and the current one kept", deleted, left)
	}
}
