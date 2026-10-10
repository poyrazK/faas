package main

// adr: 934 — dependency failure attribution: a dependency that starts
// failing after a deployment is flagged and can be the suspected dependency.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// stripeRow is one request whose payment call took a steady 40ms and
// optionally failed. status uses the raw OTLP enum on purpose: rows written
// before ingest normalized status must still count as failures.
func stripeRow(t *testing.T, deployment, tag string, created time.Time, failed bool, errorType string) sqlc.ListRequestTelemetryDependencySpansRow {
	t.Helper()
	status := "STATUS_CODE_OK"
	if failed {
		status = "STATUS_CODE_ERROR"
	}
	spans, err := json.Marshal([]debugger.StoredSpan{
		{SpanID: "s1", Name: "POST /pay", Kind: "SPAN_KIND_SERVER", DurationNanos: 50_000_000},
		{SpanID: "s2", ParentSpanID: "s1", Name: "POST", Kind: "SPAN_KIND_CLIENT", DurationNanos: 40_000_000, Status: status, ErrorType: errorType,
			Attributes: map[string]string{"http.request.method": "POST", "server.address": "api.stripe.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sqlc.ListRequestTelemetryDependencySpansRow{
		Route: "POST /pay", Method: "POST", Count: 1, Status: 200,
		ReceivedAt:   pgtype.Timestamptz{Time: created.Add(time.Minute), Valid: true},
		SpansSummary: spans, DeploymentID: deployment, DeploymentTag: tag,
		DeploymentCreatedAt: created.Format(time.RFC3339Nano),
	}
}

func stripeComparison(t *testing.T, baselineFailures, currentFailures, calls int) *api.DebugDependencyDeploymentComparison {
	t.Helper()
	v80 := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	v81 := v80.Add(time.Hour)
	var rows []sqlc.ListRequestTelemetryDependencySpansRow
	for i := 0; i < calls; i++ {
		rows = append(rows, stripeRow(t, "dep-80", "v80", v80, i < baselineFailures, "503"))
		errorType := "503"
		if i == 0 {
			errorType = "ReadTimeout"
		}
		rows = append(rows, stripeRow(t, "dep-81", "v81", v81, i < currentFailures, errorType))
	}
	got := buildDebugDependencyDeploymentComparison(rows, "", "POST /pay", debugDependencyComparisonMaxItems)
	if got == nil {
		t.Fatal("comparison = nil")
	}
	return got
}

func stripeItem(t *testing.T, comparison *api.DebugDependencyDeploymentComparison) api.DebugDependencyLatencyItem {
	t.Helper()
	for _, item := range comparison.Dependencies {
		if item.Type == debugger.AppDependencyType && item.Name == "api.stripe.com" {
			return item
		}
	}
	t.Fatalf("no api.stripe.com dependency in %+v", comparison.Dependencies)
	return api.DebugDependencyLatencyItem{}
}

func TestDependencyFailureRegression(t *testing.T) {
	for name, tc := range map[string]struct {
		baselineFailures, currentFailures, calls int
		want                                     bool
	}{
		"new failures":                  {baselineFailures: 0, currentFailures: 4, calls: 20, want: true},
		"too few failed calls":          {baselineFailures: 0, currentFailures: 2, calls: 20, want: false},
		"small rise in error rate":      {baselineFailures: 4, currentFailures: 5, calls: 40, want: false},
		"rate rose but under twice":     {baselineFailures: 8, currentFailures: 14, calls: 40, want: false},
		"rate more than doubled":        {baselineFailures: 2, currentFailures: 8, calls: 40, want: true},
		"too few calls on either side":  {baselineFailures: 0, currentFailures: 4, calls: 4, want: false},
		"unchanged steady failure rate": {baselineFailures: 6, currentFailures: 6, calls: 20, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			item := stripeItem(t, stripeComparison(t, tc.baselineFailures, tc.currentFailures, tc.calls))
			if item.FailureRegression != tc.want {
				t.Fatalf("failure_regression = %v, want %v (item %+v)", item.FailureRegression, tc.want, item)
			}
			if item.Regression {
				t.Fatalf("latency is unchanged; regression must stay false: %+v", item)
			}
		})
	}
}

func TestDependencyTopErrorTypePrefersCurrentSide(t *testing.T) {
	item := stripeItem(t, stripeComparison(t, 3, 6, 20))
	if item.TopErrorType != "503" || item.ErrorCalls != 9 {
		t.Fatalf("top_error_type=%q error_calls=%d, want 503 and 9", item.TopErrorType, item.ErrorCalls)
	}
}

func TestSuspectedDependencyFallsBackToFailures(t *testing.T) {
	suspect := suspectedDependency(stripeComparison(t, 0, 5, 20))
	if suspect == nil {
		t.Fatal("suspect = nil")
	}
	if suspect.Reason != "failures" || suspect.Name != "api.stripe.com" || suspect.Kind != "http" ||
		suspect.BaselineErrorRatePct != 0 || suspect.ErrorRatePct != 25 || suspect.ErrorType != "503" {
		t.Fatalf("suspect = %+v", suspect)
	}
	if raw := encodeSuspectedDependency(suspect); parseSuspectedDependency(raw) == nil || len(raw) > 1024 {
		t.Fatalf("suspect does not round-trip within the stored bound: %s", raw)
	}
}

func TestSuspectedDependencyPrefersLatency(t *testing.T) {
	comparison := &api.DebugDependencyDeploymentComparison{Dependencies: []api.DebugDependencyLatencyItem{
		{Type: debugger.AppDependencyType, Kind: "http", Name: "api.stripe.com", FailureRegression: true, CurrentErrorRatePct: 25},
		{Type: debugger.AppDependencyType, Kind: "postgresql", Name: "SELECT orders", Regression: true, BaselineP95MS: 80, CurrentP95MS: 190, RegressionFactor: 2.38},
	}}
	suspect := suspectedDependency(comparison)
	if suspect == nil || suspect.Reason != "latency" || suspect.Name != "SELECT orders" {
		t.Fatalf("suspect = %+v, want the latency regression", suspect)
	}
}
