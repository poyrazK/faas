//go:build !no_pg

// adr: 20261103113952

package analyticsrollup_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A client's long Unicode Referer host made Postgres refuse every flush of
// the apid that received it; refused rows now leave a gap instead, and the
// window reports incomplete.
func TestRollupsSurviveRefusedRowsAndReportTheirGaps(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "rollup-hardening@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	attacked, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "attacked"})
	bystander, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bystander"})
	rollups := receivers.NewRequestAnalyticsRollups(store, nil, nil)
	minute := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	dep := uuid.New()
	row := func(app, referrer string, n int) receivers.RequestAnalyticsRollupRow {
		return receivers.RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: uuid.MustParse(acct.ID), AppID: uuid.MustParse(app),
			DeploymentID: dep, ReceivedAt: minute, Route: "GET /", Method: "GET", Status: 200, LatencyMS: 5, Count: n, ReferrerHost: referrer}
	}
	rollups.Add(row(attacked.ID, strings.Repeat("日", 253), 1))
	rollups.Add(row(bystander.ID, "example.com", 3))
	if err := rollups.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	var referrers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM request_analytics_minutes WHERE dimension_kind = 'referrer_host'`).Scan(&referrers); err != nil || referrers != 2 {
		t.Fatalf("referrer rows = %d (%v), want both apps' rows stored", referrers, err)
	}

	// A row Postgres refuses for its data (a CHECK the writer cannot see,
	// added here) is dropped and its minute marked; the other app's rows in
	// the same flush land, and the window over that minute reports itself
	// incomplete.
	if _, err := pool.Exec(ctx, `ALTER TABLE request_analytics_minutes ADD CONSTRAINT test_refuse_status CHECK (status <> 418)`); err != nil {
		t.Fatal(err)
	}
	later := minute.Add(5 * time.Minute)
	refused := row(attacked.ID, "", 2)
	refused.Status, refused.ReceivedAt = 418, later
	fine := row(bystander.ID, "", 4)
	fine.ReceivedAt = later
	rollups.Add(refused)
	rollups.Add(fine)
	if err := rollups.Flush(ctx); err != nil {
		t.Fatalf("flush with a refused row: %v", err)
	}
	var bystanderRequests int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(requests), 0) FROM request_analytics_minutes WHERE app_id = $1 AND dimension_kind = 'route'`, bystander.ID).Scan(&bystanderRequests); err != nil || bystanderRequests != 7 {
		t.Fatalf("bystander route requests = %d (%v), want 7", bystanderRequests, err)
	}
	// Coverage began at the first flush's minute; a window starting after
	// it plus the settle lag is answered by rollups.
	from, until := minute.Add(api.RequestAnalyticsSettleLag), later.Add(time.Minute)
	yes := true
	complete, gaps := analyticsrollup.WithRollupGaps(ctx, store, nil, attacked.ID, from, until, &yes, nil)
	if complete == nil || *complete || len(gaps) != 1 || gaps[0] != analyticsrollup.WindowIncompleteGap {
		t.Fatalf("attacked app completeness = %v %v, want incomplete with %s", complete, gaps, analyticsrollup.WindowIncompleteGap)
	}
	complete, gaps = analyticsrollup.WithRollupGaps(ctx, store, nil, bystander.ID, from, until, &yes, nil)
	if complete == nil || !*complete || len(gaps) != 0 {
		t.Fatalf("bystander completeness = %v %v, want complete", complete, gaps)
	}

	// The deployment split comes from the rollups.
	split, err := analyticsrollup.RouteDeployments(ctx, store, bystander.ID, acct.ID, from.Add(-time.Hour), until)
	if err != nil || split[analyticsrollup.RouteDeploymentKey{Route: "GET /", Method: "GET", DeploymentID: dep.String()}] != 7 {
		t.Fatalf("bystander route deployments = %v (%v), want 7 on its deployment", split, err)
	}
}
