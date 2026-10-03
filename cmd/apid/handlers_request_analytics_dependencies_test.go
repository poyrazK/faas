package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
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
	if checkout.dependencies[0].ErrorRatePct != 0 || checkout.dependencies[0].P50MS != 71 || checkout.dependencies[0].P99MS != 71 {
		t.Fatalf("database latency/error summary = p50 %d p99 %d error rate %.1f%%, want 71ms/71ms/0%%", checkout.dependencies[0].P50MS, checkout.dependencies[0].P99MS, checkout.dependencies[0].ErrorRatePct)
	}
	if _, ok := got[requestAnalyticsRouteKey{route: "POST /checkout", method: "POST"}]; !ok {
		t.Fatal("GET and POST dependency evidence was not kept separate")
	}
}

func TestBuildRequestAnalyticsRouteDependenciesComparesDeploymentRegressions(t *testing.T) {
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	dependency := func(id string, revisionTime time.Time, duration time.Duration, status string) sqlc.ListRequestTelemetryDependencySpansRow {
		spans := make([]debugEvidenceSpan, 20)
		for i := range spans {
			spans[i] = debugEvidenceSpan{
				SpanID: fmt.Sprintf("%s-%d", id, i), Name: "stripe.charge", DurationNanos: uint64(duration), Status: status,
				Attributes: map[string]string{"gregale.dependency.type": "outbound_integration", "gregale.dependency.kind": "https"},
			}
		}
		return analyticsDependencyTestRow(t, "POST", "POST /checkout", 1, at, spans, id, "sha-"+id, id, revisionTime.Format(time.RFC3339Nano))
	}
	rows := []sqlc.ListRequestTelemetryDependencySpansRow{
		dependency("deploy-v1", at, 80*time.Millisecond, "ok"),
		dependency("deploy-v2", at.Add(time.Hour), 150*time.Millisecond, "error"),
	}

	got, truncated := buildRequestAnalyticsRouteDependencies(rows)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	checkout := got[requestAnalyticsRouteKey{route: "POST /checkout", method: "POST"}]
	if len(checkout.dependencies) != 1 {
		t.Fatalf("dependencies = %+v, want exactly one Stripe dependency", checkout.dependencies)
	}
	stripe := checkout.dependencies[0]
	if stripe.P50MS != 80 || stripe.P95MS != 150 || stripe.P99MS != 150 || stripe.ErrorRatePct != 50 {
		t.Fatalf("aggregate Stripe metrics = %+v, want p50/p95/p99 80/150/150ms and 50%% errors", stripe)
	}
	if len(stripe.DeploymentObservations) != 2 {
		t.Fatalf("deployment observations = %+v, want two revisions", stripe.DeploymentObservations)
	}
	current := stripe.DeploymentObservations[0]
	if current.DeploymentID != "deploy-v2" || current.DeploymentTag != "deploy-v2" || current.P95MS != 150 || current.ErrorRatePct != 100 {
		t.Fatalf("current deployment observation = %+v, want v2 at 150ms and 100%% errors", current)
	}
	if !current.Regression || current.ComparedTo != "deploy-v1" || current.P95ChangePct == nil || *current.P95ChangePct != 87.5 || current.ErrorRateChangePct == nil || *current.ErrorRateChangePct != 100 {
		t.Fatalf("current deployment comparison = %+v, want regression vs v1 (+87.5%% p95, +100pp errors)", current)
	}
	if stripe.DeploymentObservations[1].Regression || stripe.DeploymentObservations[1].ComparedTo != "" {
		t.Fatalf("baseline deployment should not have a comparison: %+v", stripe.DeploymentObservations[1])
	}
}

func TestAnnotateDependencyDeploymentRegressionsSkipsTiedOrSmallDeployments(t *testing.T) {
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	deployments := []api.RequestAnalyticsDependencyDeploymentObservation{
		{DeploymentID: "a", DeploymentTag: "a", DeploymentCreatedAt: at, Samples: 20, Calls: 20, P95MS: 100},
		{DeploymentID: "b", DeploymentTag: "b", DeploymentCreatedAt: at, Samples: 20, Calls: 20, P95MS: 200},
		{DeploymentID: "c", DeploymentTag: "c", DeploymentCreatedAt: "2026-09-25T12:00:00Z", Samples: 19, Calls: 20, P95MS: 300},
	}
	annotateDependencyDeploymentRegressions(deployments)
	for _, deployment := range deployments {
		if deployment.ComparedTo != "" || deployment.Regression {
			t.Fatalf("ambiguous or undersampled deployment got a manufactured comparison: %+v", deployment)
		}
	}
}

func analyticsDependencyTestRow(t *testing.T, method, route string, count int32, at time.Time, spans []debugEvidenceSpan, deployment ...string) sqlc.ListRequestTelemetryDependencySpansRow {
	t.Helper()
	raw, err := json.Marshal(spans)
	if err != nil {
		t.Fatalf("marshal spans: %v", err)
	}
	row := sqlc.ListRequestTelemetryDependencySpansRow{
		Route: route, Method: method, Count: count,
		Status: 200, ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true}, SpansSummary: raw,
	}
	if len(deployment) > 0 {
		row.DeploymentID = deployment[0]
	}
	if len(deployment) > 1 {
		row.CommitSha = deployment[1]
	}
	if len(deployment) > 2 {
		row.DeploymentTag = deployment[2]
	}
	if len(deployment) > 3 {
		row.DeploymentCreatedAt = deployment[3]
	}
	return row
}
