package main

// adr: 957 — dependency latency compared by deployment, not by time window.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func dependencyRow(t *testing.T, deployment, tag string, created time.Time, route string, queryMS uint64) sqlc.ListRequestTelemetryDependencySpansRow {
	t.Helper()
	spans, err := json.Marshal([]debugger.StoredSpan{
		{SpanID: "s1", Name: "GET " + route, Kind: "SPAN_KIND_SERVER", DurationNanos: (queryMS + 10) * 1_000_000},
		{SpanID: "s2", ParentSpanID: "s1", Name: "SELECT orders", Kind: "SPAN_KIND_CLIENT", DurationNanos: queryMS * 1_000_000,
			DBStatement: "SELECT * FROM orders WHERE id = 1", Attributes: map[string]string{"db.system": "postgresql"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sqlc.ListRequestTelemetryDependencySpansRow{
		Route: "GET " + route, Method: "GET", Count: 1, Status: 200,
		ReceivedAt:   pgtype.Timestamptz{Time: created.Add(time.Minute), Valid: true},
		SpansSummary: spans, DeploymentID: deployment, DeploymentTag: tag,
		DeploymentCreatedAt: created.Format(time.RFC3339Nano),
	}
}

func TestBuildDebugDependencyDeploymentComparison(t *testing.T) {
	v80 := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	v81 := v80.Add(time.Hour)
	var rows []sqlc.ListRequestTelemetryDependencySpansRow
	for i := 0; i < 12; i++ {
		rows = append(rows, dependencyRow(t, "dep-80", "v80", v80, "/checkout", 80+uint64(i%3)))
		rows = append(rows, dependencyRow(t, "dep-81", "v81", v81, "/checkout", 190+uint64(i%3)))
		rows = append(rows, dependencyRow(t, "dep-81", "v81", v81, "/health", 1))
	}
	// An older deployment must not be chosen as the comparison baseline.
	rows = append(rows, dependencyRow(t, "dep-79", "v79", v80.Add(-time.Hour), "/checkout", 500))

	got := buildDebugDependencyDeploymentComparison(rows, "", "GET /checkout", debugDependencyComparisonMaxItems)
	if got == nil {
		t.Fatal("comparison = nil")
	}
	if got.CurrentDeploymentID != "dep-81" || got.PreviousDeploymentID != "dep-80" || got.PreviousDeploymentTag != "v80" || got.Route != "GET /checkout" {
		t.Fatalf("deployments = %+v", got)
	}
	var found bool
	for _, item := range got.Dependencies {
		if item.Type != debugger.AppDependencyType {
			continue
		}
		found = true
		if item.Kind != "postgresql" || item.Name != "SELECT orders" || !item.Regression {
			t.Fatalf("dependency = %+v", item)
		}
		if item.BaselineP95MS < 80 || item.BaselineP95MS > 83 || item.CurrentP95MS < 190 || item.CurrentP95MS > 193 {
			t.Fatalf("p95 baseline=%d current=%d", item.BaselineP95MS, item.CurrentP95MS)
		}
	}
	if !found {
		t.Fatalf("no app_dependency item in %+v", got.Dependencies)
	}
	if len(got.Dependencies) == 0 || !got.Dependencies[0].Regression {
		t.Fatalf("regressions must sort first: %+v", got.Dependencies)
	}

	// Comparing an explicit older deployment picks the one before it.
	older := buildDebugDependencyDeploymentComparison(rows, "dep-80", "GET /checkout", 0)
	if older == nil || older.CurrentDeploymentID != "dep-80" || older.PreviousDeploymentID != "dep-79" {
		t.Fatalf("explicit comparison = %+v", older)
	}
}

// adr: 957 — the regression detector names the regressed dependency.
func TestSuspectedDependencyBetweenDetectorDeployments(t *testing.T) {
	v80 := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	v81 := v80.Add(time.Hour)
	v82 := v81.Add(time.Hour)
	var rows []sqlc.ListRequestTelemetryDependencySpansRow
	for i := 0; i < 8; i++ {
		rows = append(rows, dependencyRow(t, "dep-80", "v80", v80, "/checkout", 80))
		rows = append(rows, dependencyRow(t, "dep-81", "v81", v81, "/checkout", 190))
		// A newer deployment the detector is not comparing must be ignored.
		rows = append(rows, dependencyRow(t, "dep-82", "v82", v82, "/checkout", 20))
	}
	comparison := buildDebugDependencyComparisonBetween(rows, "dep-81", "dep-80", "GET /checkout", 0)
	if comparison == nil || comparison.CurrentDeploymentID != "dep-81" || comparison.PreviousDeploymentID != "dep-80" {
		t.Fatalf("comparison = %+v", comparison)
	}
	suspect := suspectedDependency(comparison)
	if suspect == nil || suspect.Type != "app_dependency" || suspect.Kind != "postgresql" || suspect.Name != "SELECT orders" ||
		suspect.P95BaseMS != 80 || suspect.P95MS != 190 {
		t.Fatalf("suspect = %+v", suspect)
	}
	raw := encodeSuspectedDependency(suspect)
	if got := parseSuspectedDependency(raw); got == nil || *got != *suspect {
		t.Fatalf("round trip = %+v from %s", got, raw)
	}
	if buildDebugDependencyComparisonBetween(rows, "dep-81", "dep-79", "GET /checkout", 0) != nil {
		t.Fatal("missing baseline deployment produced a comparison")
	}
}

func TestSuspectedDependencySkipsApplicationSpansAndNil(t *testing.T) {
	if suspectedDependency(nil) != nil || encodeSuspectedDependency(nil) != nil || parseSuspectedDependency(nil) != nil {
		t.Fatal("nil inputs must stay nil")
	}
	comparison := &api.DebugDependencyDeploymentComparison{Dependencies: []api.DebugDependencyLatencyItem{
		{Type: "application", Name: "render", Regression: true, BaselineP95MS: 10, CurrentP95MS: 90},
		{Type: "app_dependency", Kind: "redis", Name: "GET", Regression: false},
	}}
	if got := suspectedDependency(comparison); got != nil {
		t.Fatalf("suspect = %+v, want none", got)
	}
	if parseSuspectedDependency([]byte(`{"type":"app_dependency"}`)) != nil {
		t.Fatal("suspect without a name accepted")
	}
	if parseSuspectedDependency([]byte(`not json`)) != nil {
		t.Fatal("invalid json accepted")
	}
}

func TestBuildDebugDependencyDeploymentComparisonNeedsTwoDeployments(t *testing.T) {
	created := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	rows := []sqlc.ListRequestTelemetryDependencySpansRow{dependencyRow(t, "dep-1", "v1", created, "/checkout", 50)}
	if got := buildDebugDependencyDeploymentComparison(rows, "", "", 0); got != nil {
		t.Fatalf("single deployment comparison = %+v", got)
	}
	if got := buildDebugDependencyDeploymentComparison(rows, "missing", "", 0); got != nil {
		t.Fatalf("unknown deployment comparison = %+v", got)
	}
	if got := buildDebugDependencyDeploymentComparison(nil, "", "", 0); got != nil {
		t.Fatalf("empty comparison = %+v", got)
	}
}
