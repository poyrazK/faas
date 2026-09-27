package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestBuildRequestAnalyticsRouteDependenciesGroupsByRouteAndDependency(t *testing.T) {
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	rows := []sqlc.ListRequestTelemetryDependencySpansRow{
		analyticsDependencyTestRow(t, "GET", "GET /checkout", 2, at, []debugEvidenceSpan{
			{
				SpanID: "handler", Name: "handler", StartTimeUnixNano: uint64(at.UnixNano()),
				EndTimeUnixNano: uint64(at.Add(100 * time.Millisecond).UnixNano()), DurationNanos: uint64(100 * time.Millisecond),
			},
			{
				SpanID: "db", ParentSpanID: "handler", Name: "db.query", StartTimeUnixNano: uint64(at.UnixNano()),
				EndTimeUnixNano: uint64(at.Add(71 * time.Millisecond).UnixNano()), DurationNanos: uint64(71 * time.Millisecond),
				Attributes: map[string]string{"gregale.dependency.type": "managed_binding", "gregale.dependency.kind": "managed_postgres"},
			},
			{
				SpanID: "stripe", ParentSpanID: "handler", Name: "stripe.charge", StartTimeUnixNano: uint64(at.UnixNano()),
				EndTimeUnixNano: uint64(at.Add(49 * time.Millisecond).UnixNano()), DurationNanos: uint64(49 * time.Millisecond),
				Attributes: map[string]string{"gregale.dependency.type": "outbound_integration", "gregale.dependency.kind": "https"},
			},
		}),
		analyticsDependencyTestRow(t, "POST", "POST /checkout", 1, at, []debugEvidenceSpan{
			{Name: "db.query", DurationNanos: uint64(30 * time.Millisecond), Attributes: map[string]string{"gregale.dependency.type": "managed_binding", "gregale.dependency.kind": "managed_postgres"}},
		}),
	}

	got, truncated := buildRequestAnalyticsRouteDependencies(rows)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	checkout, ok := got[requestAnalyticsRouteKey{route: "GET /checkout", method: "GET"}]
	if !ok {
		t.Fatal("missing GET /checkout dependency data")
	}
	if checkout.samples != 2 || checkout.requests != 2 || len(checkout.dependencies) != 2 {
		t.Fatalf("GET /checkout aggregate = %+v, want 2 span samples, 2 represented requests, 2 dependencies", checkout)
	}
	if checkout.dependencies[0].Kind != "managed_postgres" || checkout.dependencies[0].P95MS != 71 || checkout.dependencies[0].ExclusiveP95MS != 71 {
		t.Fatalf("database dependency = %+v, want 71ms p95 and exclusive p95", checkout.dependencies[0])
	}
	if checkout.dependencies[0].Samples != 1 || checkout.dependencies[0].Calls != 2 {
		t.Fatalf("database sample/call counts = (%d, %d), want (1 retained span, 2 weighted calls)", checkout.dependencies[0].Samples, checkout.dependencies[0].Calls)
	}
	if _, ok := got[requestAnalyticsRouteKey{route: "POST /checkout", method: "POST"}]; !ok {
		t.Fatal("GET and POST dependency evidence was not kept separate")
	}
}

func analyticsDependencyTestRow(t *testing.T, method, route string, count int32, at time.Time, spans []debugEvidenceSpan) sqlc.ListRequestTelemetryDependencySpansRow {
	t.Helper()
	raw, err := json.Marshal(spans)
	if err != nil {
		t.Fatalf("marshal spans: %v", err)
	}
	return sqlc.ListRequestTelemetryDependencySpansRow{
		Route: route, Method: method, Count: count,
		Status: 200, ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true}, SpansSummary: raw,
	}
}
