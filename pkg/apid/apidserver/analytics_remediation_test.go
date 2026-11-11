package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type groupedAnalyticsContractStore struct{ Store state.Store }

func (s *groupedAnalyticsContractStore) RequestTelemetryAnalyticsTimeseriesGrouped(context.Context, sqlc.RequestTelemetryAnalyticsTimeseriesGroupedParams) ([]sqlc.RequestTelemetryAnalyticsTimeseriesGroupedRow, error) {
	return []sqlc.RequestTelemetryAnalyticsTimeseriesGroupedRow{{BucketStart: pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Hour), Valid: true}, Dimension: "DE", Requests: 1}}, nil
}

func TestAnalyticsGroupedHTTPResponseHasArrayPoints(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "grouped-contract", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	*(*e.S()).Store() = statetest.NewStore(&groupedAnalyticsContractStore{Store: *e.Store()})
	r := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/analytics/timeseries?since=1h&group_by=country", nil, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", r.Code, r.Body)
	}
	var wire map[string]any
	if err = json.Unmarshal(r.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if points, ok := wire["points"].([]any); !ok || len(points) != 0 {
		t.Fatalf("grouped points must be an empty array: %#v", wire["points"])
	}
}

func TestAnalyticsHealthyRollupsSurviveLockedRawEnrichments(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(t.Context(), "analytics-enrichment-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "enrichment-timeout"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	at := now.Add(-10 * time.Minute)
	row := state.RequestAnalyticsRollup{AppID: uuid.MustParse(app.ID), AccountID: uuid.MustParse(acct.ID), DeploymentID: uuid.New(), BucketStart: at, DimensionKind: "route", Dimension: "GET /", Method: "GET", Status: 200, Requests: 100}
	row.LatencyHist[0] = 100
	if err = store.AddRequestAnalyticsRollups(t.Context(), []state.RequestAnalyticsRollup{row}, []state.RequestAnalyticsCoverage{{AppID: row.AppID, CoveredSince: now.Add(-2 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(t.Context()) }()
	if _, err = lock.Exec(t.Context(), "LOCK TABLE request_telemetry IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	response, err := apidserver.RequestAnalyticsResponseForTest(t.Context(), store, app, acct, now.Add(-time.Hour), now)
	if err != nil || response.Requests != 100 || !slices.Contains(response.Omitted, "dependencies") {
		t.Fatalf("healthy counts lost: %+v err=%v", response, err)
	}
}

func TestAnalyticsDeploymentSamplesDoNotInventOtherTraffic(t *testing.T) {
	routes := []api.RequestAnalyticsRoute{{Route: "GET /", Method: "GET", Requests: 100, EstimatedComputeCostMillicents: 1000}}
	rows := []sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow{{Route: "GET /", Method: "GET", DeploymentID: "only-deployment", Requests: 10}}
	apidserver.AttachRequestAnalyticsRouteDeploymentObservations(routes, rows, 100)
	if routes[0].OtherDeploymentRequests != 0 || routes[0].OtherDeploymentEstimatedComputeCostMillicents != 0 || routes[0].DeploymentObservations[0].EstimatedComputeCostMillicents != 100 {
		t.Fatalf("fabricated deployment attribution: %+v", routes[0])
	}
}

func TestLogDrainAnalyticsIncludesOpeningHourAndMarksUnknownBaselines(t *testing.T) {
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	samples := []state.AppLogDrainDeliveryAnalytics{
		{BucketStart: base.Add(-2 * time.Hour), SampledAt: base.Add(-time.Hour - time.Second)},
		{BucketStart: base.Add(-time.Hour), SampledAt: base.Add(-time.Second), DeliveredTotal: 100},
		{BucketStart: base, SampledAt: base.Add(10 * time.Minute), DeliveredTotal: 105},
	}
	out := apidserver.AppLogDrainAnalyticsResponseForTest(samples, "drain", "1h", base.Add(-50*time.Minute), base.Add(10*time.Minute))
	if out.Summary.Delivered != 105 || out.From != "2026-11-11T10:59:59Z" || !out.WindowAligned || out.Complete == nil || !*out.Complete {
		t.Fatalf("opening hour was lost or window misreported: %+v", out)
	}
	out = apidserver.AppLogDrainAnalyticsResponseForTest(samples[1:], "drain", "1h", base.Add(-50*time.Minute), base.Add(10*time.Minute))
	if out.Summary.Delivered != 5 || out.Complete == nil || *out.Complete || !slices.Contains(out.CoverageGaps, "baseline_missing") {
		t.Fatalf("unknown baseline counted lifetime: %+v", out)
	}
}
