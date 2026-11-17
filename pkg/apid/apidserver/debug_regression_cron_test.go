package apidserver_test

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCountAffectedRequestsWeightsCollapsedRows(t *testing.T) {
	rows := []sqlc.RequestTelemetryLatencyWeightsByRouteRow{
		{Route: "GET /slow", LatencyMs: 201, Requests: 100},
		{Route: "GET /slow", LatencyMs: 200, Requests: 2},
		{Route: "GET /slow", LatencyMs: 100, Requests: 999},
		{Route: "GET /other", LatencyMs: 500, Requests: 1000},
	}

	if got := apidserver.CountAffectedRequests(rows, "GET /slow", 200); got != 100 {
		t.Fatalf("countAffectedRequests = %d, want 100", got)
	}
}

func TestCountAffectedRequestsSaturatesInt32(t *testing.T) {
	rows := []sqlc.RequestTelemetryLatencyWeightsByRouteRow{
		{Route: "GET /slow", LatencyMs: 201, Requests: math.MaxInt32},
		{Route: "GET /slow", LatencyMs: 202, Requests: math.MaxInt32},
	}

	if got := apidserver.CountAffectedRequests(rows, "GET /slow", 200); got != math.MaxInt32 {
		t.Fatalf("countAffectedRequests = %d, want %d", got, math.MaxInt32)
	}
}

// The route cap keeps the busiest routes, not the first alphabetically, so a
// busy /v1/* route is never crowded out by quiet /admin/* routes.
func TestRegressionBaselineRoutesKeepsBusiest(t *testing.T) {
	baseline := []sqlc.RequestTelemetryBaselineP95ByRouteRow{
		{Route: "GET /admin/a", P95Ms: 10, N: 5},
		{Route: "GET /admin/b", P95Ms: 10, N: 5},
		{Route: "GET /v1/orders", P95Ms: 80, N: 90_000},
		{Route: "GET /v1/users", P95Ms: 40, N: 1_000},
	}
	got := apidserver.RegressionBaselineRoutes(baseline, 2)
	if len(got) != 2 || got["GET /v1/orders"] != 80 || got["GET /v1/users"] != 40 {
		t.Fatalf("kept routes = %v, want the two busiest", got)
	}
	if all := apidserver.RegressionBaselineRoutes(baseline, 10); len(all) != 4 {
		t.Fatalf("under the cap kept %d routes, want all 4", len(all))
	}
}
