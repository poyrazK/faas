package apidserver_test

// adr: 127 — error-rate route regressions (amendment): the detector records
// the regressed signal on one observation per (deployment, route).

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func numericString(t *testing.T, n pgtype.Numeric) string {
	t.Helper()
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		t.Fatalf("numeric %+v: %v", n, err)
	}
	return fmt.Sprintf("%.2f", f.Float64)
}

func TestDebugRegressionParamsErrorsOnly(t *testing.T) {
	// The p95 improved: an errors-only observation stores factor 1.00, since
	// regression_factor is CHECKed ≥ 1.0, and reports failed requests as
	// affected.
	params, err := apidserver.DebugRegressionParams(pgtype.UUID{}, pgtype.UUID{}, "POST /pay", debugger.RegressionKindErrors,
		200, 150, 61, debugger.RouteErrors{Requests: 500, Errors: 2}, debugger.RouteErrors{Requests: 488, Errors: 61})
	if err != nil {
		t.Fatal(err)
	}
	if params.Kind != "errors" || params.ErrorCount != 61 || params.AffectedCount != 61 {
		t.Fatalf("params = %+v", params)
	}
	if got := numericString(t, params.RegressionFactor); got != "1.00" {
		t.Fatalf("regression_factor = %s, want 1.00", got)
	}
	if got := numericString(t, params.ErrorRatePct); got != "12.50" {
		t.Fatalf("error_rate_pct = %s, want 12.50", got)
	}
	if got := numericString(t, params.ErrorRateBasePct); got != "0.40" {
		t.Fatalf("error_rate_base_pct = %s, want 0.40", got)
	}
}

func TestDebugRegressionParamsLatencyKeepsFactor(t *testing.T) {
	params, err := apidserver.DebugRegressionParams(pgtype.UUID{}, pgtype.UUID{}, "GET /", debugger.RegressionKindLatencyAndErrors,
		200, 420, 150, debugger.RouteErrors{}, debugger.RouteErrors{Requests: 300, Errors: 30})
	if err != nil {
		t.Fatal(err)
	}
	if got := numericString(t, params.RegressionFactor); got != "2.10" || params.AffectedCount != 150 {
		t.Fatalf("factor=%s affected=%d, want 2.10 and the latency count", got, params.AffectedCount)
	}
}

func TestDebugRegressionCandidateRoutes(t *testing.T) {
	baselineP95 := map[string]int32{"GET /a": 10}
	currentP95 := map[string]int32{"GET /a": 30, "GET /new": 5}
	baselineErrors := map[string]debugger.RouteErrors{"POST /pay": {Requests: 100}}
	currentErrors := map[string]debugger.RouteErrors{"POST /pay": {Requests: 100, Errors: 40}, "GET /a": {Requests: 9}, "GET /other": {Requests: 3}}
	got := apidserver.DebugRegressionCandidateRoutes(baselineP95, currentP95, baselineErrors, currentErrors)
	// Routes without a baseline for either signal cannot regress.
	if want := []string{"GET /a", "POST /pay"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
	// Without error-rate rows (no traffic counted in a window) the latency routes are still walked.
	if got := apidserver.DebugRegressionCandidateRoutes(baselineP95, currentP95, nil, nil); !reflect.DeepEqual(got, []string{"GET /a"}) {
		t.Fatalf("latency-only routes = %v", got)
	}
}

func TestSuspectedDependencyForPrefersFailures(t *testing.T) {
	comparison := &api.DebugDependencyDeploymentComparison{Dependencies: []api.DebugDependencyLatencyItem{
		{Type: debugger.AppDependencyType, Kind: "postgresql", Name: "SELECT orders", Regression: true, BaselineP95MS: 80, CurrentP95MS: 190, RegressionFactor: 2.38},
		{Type: debugger.AppDependencyType, Kind: "http", Name: "api.stripe.com", FailureRegression: true, CurrentErrorRatePct: 25, TopErrorType: "503"},
	}}
	if got := apidserver.SuspectedDependencyFor(comparison, true); got == nil || got.Reason != "failures" || got.Name != "api.stripe.com" {
		t.Fatalf("errors-only suspect = %+v, want the failing dependency", got)
	}
	if got := apidserver.SuspectedDependencyFor(comparison, false); got == nil || got.Reason != "latency" || got.Name != "SELECT orders" {
		t.Fatalf("latency suspect = %+v, want the slower dependency", got)
	}
	slowOnly := &api.DebugDependencyDeploymentComparison{Dependencies: comparison.Dependencies[:1]}
	if got := apidserver.SuspectedDependencyFor(slowOnly, true); got == nil || got.Reason != "latency" {
		t.Fatalf("errors-only regression falls back to a slow dependency, got %+v", got)
	}
}

func TestDebugRegressionObservationToItemSignal(t *testing.T) {
	var rate, base pgtype.Numeric
	if err := rate.Scan("12.50"); err != nil {
		t.Fatal(err)
	}
	if err := base.Scan("0.40"); err != nil {
		t.Fatal(err)
	}
	item := apidserver.DebugRegressionObservationToItem(sqlc.DebugRegressionObservation{
		Route: "POST /pay", Kind: "errors", ErrorRatePct: rate, ErrorRateBasePct: base, ErrorCount: 61,
	})
	if item.Kind != "errors" || item.ErrorRatePct != 12.5 || item.ErrorRateBasePct != 0.4 || item.ErrorCount != 61 {
		t.Fatalf("item = %+v", item)
	}
	// Rows from before error-rate detection carry no kind and read as latency.
	if legacy := apidserver.DebugRegressionObservationToItem(sqlc.DebugRegressionObservation{Route: "GET /"}); legacy.Kind != "latency" {
		t.Fatalf("legacy kind = %q, want latency", legacy.Kind)
	}
}
