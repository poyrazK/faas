//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 127 — amendment 2026-11-05 against Postgres:
//   - the regression view leaves out cold boots, platform refusals, pinned
//     requests and long-lived streams;
//   - the export applies the list's filters and says when its limit cut it;
//   - coverage reports the requests analytics counted that were not kept;
//   - the deployment list carries the release time the detector orders by.
func TestDebugReviewRound2PG(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "round2-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("2", 64), Scope: "default", Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	appID := pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}
	depID := pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true}
	created := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	base := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Minute)

	type row struct {
		status, latency                  int32
		cold, refusal, pinned, longLived bool
	}
	rows := []row{
		{status: 200, latency: 10},
		{status: 200, latency: 10},
		{status: 500, latency: 12},
		{status: 200, latency: 9000, cold: true},
		{status: 503, latency: 1, refusal: true},
		{status: 200, latency: 9000, pinned: true},
		{status: 200, latency: 600000, longLived: true},
	}
	for i, r := range rows {
		if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse(app.AccountID), Valid: true}, AppID: appID, DeploymentID: depID,
			ReceivedAt: pgtype.Timestamptz{Time: base.Add(time.Duration(i) * time.Second), Valid: true},
			Route:      "GET /checkout", Method: "GET", Status: r.status, LatencyMs: r.latency, Count: 1,
			ColdBoot: r.cold, PlatformRefusal: r.refusal, DeploymentPinned: r.pinned, LongLived: r.longLived,
			DeploymentCreatedAt: created,
			UaFamily:            "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
		}); err != nil {
			t.Fatal(err)
		}
	}
	from := pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true}
	until := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	t.Run("regression view", func(t *testing.T) {
		all, err := store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{AppID: appID, DeploymentID: depID, ReceivedAt: from, ReceivedAt_2: until})
		if err != nil || len(all) != 1 || all[0].Requests != 7 || all[0].ErrorRequests != 2 {
			t.Fatalf("all errors = %+v err=%v, want 7 requests, 2 errors", all, err)
		}
		view, err := store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{AppID: appID, DeploymentID: depID, ReceivedAt: from, ReceivedAt_2: until, Column5: true})
		if err != nil || len(view) != 1 || view[0].Requests != 5 || view[0].ErrorRequests != 1 {
			t.Fatalf("regression-view errors = %+v err=%v, want 5 requests and only the app's 500", view, err)
		}
		p95, err := store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{AppID: appID, DeploymentID: depID, ReceivedAt: from, ReceivedAt_2: until, Column5: true})
		if err != nil || len(p95) != 1 || p95[0].P95Ms > 12 {
			t.Fatalf("regression-view p95 = %+v err=%v, want the warm requests' latency only", p95, err)
		}
		drill, err := store.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{AppID: appID, DeploymentID: depID, ReceivedAt: from, ReceivedAt_2: until, Limit: 100, Column6: true})
		if err != nil || len(drill) != 3 {
			t.Fatalf("regression-view drilldown = %d rows err=%v, want the 3 warm deployment-served rows", len(drill), err)
		}
	})

	t.Run("deployment release time", func(t *testing.T) {
		deps, err := store.ListDeploymentsForCompare(ctx, sqlc.ListDeploymentsForCompareParams{AppID: appID, Column2: pgtype.Interval{Microseconds: int64(24 * time.Hour / time.Microsecond), Valid: true}, Limit: 10})
		if err != nil || len(deps) != 1 || deps[0].DeploymentCreatedAt != created {
			t.Fatalf("deployments = %+v err=%v, want the release time %s", deps, err, created)
		}
	})

	t.Run("export filters and truncation", func(t *testing.T) {
		rec := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/debug/requests/export?since=1h&status=200&cold_boot=false&limit=2", nil, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("X-Faas-Request-Log-Truncated") != "true" {
			t.Fatalf("export = %d truncated=%q body=%s", rec.Code, rec.Header().Get("X-Faas-Request-Log-Truncated"), rec.Body)
		}
		lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("export lines = %d, want the 2-row limit", len(lines))
		}
		for _, line := range lines {
			var item api.DebugTelemetryRequestItem
			if err := json.Unmarshal([]byte(line), &item); err != nil || item.Status != 200 || item.ColdBoot {
				t.Fatalf("exported %s, want only warm 200s", line)
			}
		}
		rec = e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/debug/requests/export?since=1h&status=500", nil, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("X-Faas-Request-Log-Truncated") != "false" || strings.Count(strings.TrimSpace(rec.Body.String()), "\n") != 0 {
			t.Fatalf("filtered export = %d truncated=%q body=%s, want the one 500", rec.Code, rec.Header().Get("X-Faas-Request-Log-Truncated"), rec.Body)
		}
	})

	t.Run("requests not retained", func(t *testing.T) {
		rollups, ok := store.(state.RequestAnalyticsRollupStore)
		if !ok {
			t.Fatal("store has no analytics rollups")
		}
		// Analytics counted 12 requests in the rows' minute; the debugger
		// kept 7.
		if err := rollups.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{{
			AppID: uuid.MustParse(app.ID), AccountID: uuid.MustParse(app.AccountID), DeploymentID: uuid.MustParse(dep.ID),
			BucketStart: base, DimensionKind: "route", Dimension: "/checkout", Method: "GET", Status: 200, Requests: 12,
		}}, []state.RequestAnalyticsCoverage{{AppID: uuid.MustParse(app.ID), CoveredSince: base.Add(-time.Hour)}}); err != nil {
			t.Fatal(err)
		}
		var coverage api.DebugCoverageResponse
		rec := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/debug/coverage?since=30m", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("coverage = %d %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &coverage); err != nil {
			t.Fatal(err)
		}
		if coverage.RequestsNotRetained == nil || *coverage.RequestsNotRetained != 5 {
			t.Fatalf("requests_not_retained = %v, want 5", coverage.RequestsNotRetained)
		}
	})
}
