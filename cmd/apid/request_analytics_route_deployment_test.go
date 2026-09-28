package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestAttachRequestAnalyticsRouteDeploymentObservationsAllocatesAndCompares(t *testing.T) {
	routes := []api.RequestAnalyticsRoute{
		{Route: "POST /checkout", Method: "POST", Requests: 55, EstimatedComputeCostMillicents: 101},
		{Route: "GET /health", Method: "GET", Requests: 45, EstimatedComputeCostMillicents: 99},
	}
	rows := []sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow{
		{
			Route: "POST /checkout", Method: "POST", DeploymentID: "deploy-v2", CommitSha: "sha-v2", DeploymentTag: "v2",
			DeploymentCreatedAt: "2026-03-02T12:00:00Z", Requests: 30, GuestCpuMeasuredRequests: 30, GuestCpuAvgMs: 15,
		},
		{
			Route: "POST /checkout", Method: "POST", DeploymentID: "deploy-v1", CommitSha: "sha-v1", DeploymentTag: "v1",
			DeploymentCreatedAt: "2026-03-01T12:00:00Z", Requests: 20, GuestCpuMeasuredRequests: 20, GuestCpuAvgMs: 10,
		},
		{Route: "POST /checkout", Method: "POST", DeploymentID: "__other__", Requests: 5},
		{
			Route: "GET /health", Method: "GET", DeploymentID: "deploy-health", DeploymentTag: "health-v1",
			DeploymentCreatedAt: "2026-03-03T12:00:00Z", Requests: 45,
		},
	}

	attachRequestAnalyticsRouteDeploymentObservations(routes, rows, 100)

	checkout := routes[0]
	if len(checkout.DeploymentObservations) != 2 || checkout.OtherDeploymentRequests != 5 {
		t.Fatalf("checkout deployment split = %+v / other=%d, want two deployments and five other requests", checkout.DeploymentObservations, checkout.OtherDeploymentRequests)
	}
	byID := make(map[string]api.RequestAnalyticsRouteDeploymentObservation, len(checkout.DeploymentObservations))
	for _, observation := range checkout.DeploymentObservations {
		byID[observation.DeploymentID] = observation
	}
	v1, v2 := byID["deploy-v1"], byID["deploy-v2"]
	if v1.EstimatedComputeCostMillicents != 37 || v2.EstimatedComputeCostMillicents != 55 || checkout.OtherDeploymentEstimatedComputeCostMillicents != 9 {
		t.Fatalf("route cost allocation = v1:%d v2:%d other:%d, want 37/55/9", v1.EstimatedComputeCostMillicents, v2.EstimatedComputeCostMillicents, checkout.OtherDeploymentEstimatedComputeCostMillicents)
	}
	if v1.RequestSharePct != 20 || v2.RequestSharePct != 30 {
		t.Fatalf("app request shares = v1:%.2f%% v2:%.2f%%, want 20%%/30%%", v1.RequestSharePct, v2.RequestSharePct)
	}
	if v1.GuestCPUChangePct != nil || v1.GuestCPURegression {
		t.Fatalf("baseline observation unexpectedly has a CPU comparison: %+v", v1)
	}
	if v2.GuestCPUChangePct == nil || *v2.GuestCPUChangePct != 50 || v2.GuestCPUComparedTo != "v1" || !v2.GuestCPURegression {
		t.Fatalf("v2 route CPU comparison = %+v, want +50%% regression vs v1", v2)
	}
	if routes[1].DeploymentObservations[0].GuestCPURegression {
		t.Fatalf("a different route's deployment should not inherit checkout regression: %+v", routes[1].DeploymentObservations[0])
	}
}

func TestAnnotateRouteDeploymentCPURegressionsSkipsAmbiguousAndSmallSamples(t *testing.T) {
	cpu := func(value int) *int { return &value }
	observations := []api.RequestAnalyticsRouteDeploymentObservation{
		{DeploymentID: "same-a", DeploymentCreatedAt: "2026-03-01T12:00:00Z", GuestCPUAvgMS: cpu(10), GuestCPUMeasuredRequests: 20},
		{DeploymentID: "same-b", DeploymentCreatedAt: "2026-03-01T12:00:00Z", GuestCPUAvgMS: cpu(20), GuestCPUMeasuredRequests: 20},
		{DeploymentID: "small", DeploymentCreatedAt: "2026-03-02T12:00:00Z", GuestCPUAvgMS: cpu(30), GuestCPUMeasuredRequests: 19},
	}

	annotateRouteDeploymentCPURegressions(observations)

	for _, observation := range observations {
		if observation.GuestCPUChangePct != nil || observation.GuestCPURegression {
			t.Fatalf("ambiguous or undersized route sample produced a CPU comparison: %+v", observation)
		}
	}
}
