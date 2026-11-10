//go:build !no_pg

// adr: 20261103113952

package analyticsrollup_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func insertRawTelemetry(t *testing.T, pool *pgxpool.Pool, accountID, appID uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO request_telemetry (account_id, app_id, deployment_id, route, method, status, latency_ms, received_at)
		VALUES ($1, $2, $3, 'GET /', 'GET', 200, 5, $4)`, accountID, appID, uuid.New(), at); err != nil {
		t.Fatal(err)
	}
}

// An app with no raw telemetry before its rollups began had no traffic
// they miss: its long windows read rollups, both tiers, instead of falling
// back to raw rows. Raw rows before the coverage keep the fallback
// (amendment 4).
func TestRollupCoverageIsBackdatedOverSpansWithoutTraffic(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-backdate@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-backdate"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	now := time.Now().UTC()
	// The app's first request came 3 days ago, at minute 23 of its hour.
	first := now.Truncate(time.Hour).Add(-72*time.Hour + 23*time.Minute)
	row := state.RequestAnalyticsRollup{
		AppID: appID, AccountID: accountID, DeploymentID: uuid.New(), BucketStart: first,
		DimensionKind: "route", Dimension: "GET /", Method: "GET", Status: 200, Requests: 7, LatencyMaxMS: 9,
	}
	row.LatencyHist[0] = 7
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{row}, []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: first}}); err != nil {
		t.Fatal(err)
	}
	insertRawTelemetry(t, pool, accountID, appID, first.Add(10*time.Second))

	// A 7-day window, aligned as the apid parser aligns it.
	from := now.Truncate(time.Hour).Add(-7 * 24 * time.Hour)
	reader, ok := analyticsrollup.Covers(ctx, store, app.ID, from)
	if !ok {
		t.Fatal("rollups do not answer a 7-day window of an app with no earlier traffic")
	}
	groups, err := analyticsrollup.Read(ctx, reader, analyticsrollup.Query{AppID: app.ID, AccountID: account.ID, From: from, Until: now, GroupBy: "route"})
	if err != nil {
		t.Fatal(err)
	}
	if got := analyticsrollup.Summary(groups).Requests; got != 7 {
		t.Fatalf("7-day requests = %d, want the first hour's 7 from the hourly tier", got)
	}

	// Raw telemetry before the coverage: the rollups miss it, so raw rows
	// answer.
	insertRawTelemetry(t, pool, accountID, appID, first.Add(-24*time.Hour))
	if _, ok := analyticsrollup.Covers(ctx, store, app.ID, from); ok {
		t.Fatal("rollups answer a window holding raw traffic they do not have")
	}
	if _, ok := analyticsrollup.Covers(ctx, store, app.ID, first.Add(-12*time.Hour).Truncate(time.Hour)); !ok {
		t.Fatal("rollups do not answer a window starting after that traffic")
	}
}

// After an apid stopped without its final flush, every app minute with raw
// telemetry in the span is marked lost, and analytics over it reports the
// window incomplete.
func TestRollupGapsMarkedFromRawTelemetry(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-crash@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-crash"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	minute := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	insertRawTelemetry(t, pool, accountID, appID, minute.Add(15*time.Second))
	insertRawTelemetry(t, pool, accountID, appID, minute.Add(5*time.Minute)) // outside the span
	if err := store.RecordRequestAnalyticsRollupGapsFromTelemetry(ctx, minute.Add(-time.Minute), minute.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if has, err := store.RequestAnalyticsRollupHasGap(ctx, appID, minute, minute.Add(time.Minute)); err != nil || !has {
		t.Fatalf("gap over the crashed minute = %v (%v), want marked", has, err)
	}
	if has, err := store.RequestAnalyticsRollupHasGap(ctx, appID, minute.Add(5*time.Minute), minute.Add(6*time.Minute)); err != nil || has {
		t.Fatalf("gap outside the span = %v (%v), want none", has, err)
	}
}
