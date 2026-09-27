package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAnnotateDeploymentCPURegressionsUsesChronologyAndSampleThreshold(t *testing.T) {
	cpu := func(value int) *int { return &value }
	deployments := []api.RequestAnalyticsDeploymentCost{
		{DeploymentID: "v3-id", DeploymentTag: "v3", DeploymentCreatedAt: "2026-03-03T12:00:00Z", GuestCPUAvgMS: cpu(15), GuestCPUMeasuredRequests: 30},
		{DeploymentID: "v1-id", DeploymentTag: "v1", DeploymentCreatedAt: "2026-03-01T12:00:00Z", GuestCPUAvgMS: cpu(10), GuestCPUMeasuredRequests: 20},
		{DeploymentID: "small-id", DeploymentTag: "small", DeploymentCreatedAt: "2026-03-04T12:00:00Z", GuestCPUAvgMS: cpu(40), GuestCPUMeasuredRequests: 19},
		{DeploymentID: "v2-id", DeploymentTag: "v2", DeploymentCreatedAt: "2026-03-02T12:00:00Z", GuestCPUAvgMS: cpu(12), GuestCPUMeasuredRequests: 20},
	}

	annotateDeploymentCPURegressions(deployments)

	if deployments[1].GuestCPUChangePct != nil || deployments[1].GuestCPURegression {
		t.Fatalf("baseline deployment unexpectedly has a CPU comparison: %+v", deployments[1])
	}
	if deployments[3].GuestCPUChangePct == nil || *deployments[3].GuestCPUChangePct != 20 {
		t.Fatalf("v2 change = %v, want 20%%", deployments[3].GuestCPUChangePct)
	}
	if deployments[3].GuestCPUComparedTo != "v1" || deployments[3].GuestCPURegression {
		t.Fatalf("v2 comparison = %+v, want non-regression vs v1", deployments[3])
	}
	if deployments[0].GuestCPUChangePct == nil || *deployments[0].GuestCPUChangePct != 25 {
		t.Fatalf("v3 change = %v, want 25%%", deployments[0].GuestCPUChangePct)
	}
	if deployments[0].GuestCPUComparedTo != "v2" || !deployments[0].GuestCPURegression {
		t.Fatalf("v3 comparison = %+v, want regression vs v2", deployments[0])
	}
	if deployments[2].GuestCPUChangePct != nil || deployments[2].GuestCPURegression {
		t.Fatalf("small sample should not produce a comparison: %+v", deployments[2])
	}
}

func TestAnnotateDeploymentCPURegressionsSkipsAmbiguousCreationTimes(t *testing.T) {
	cpu := func(value int) *int { return &value }
	deployments := []api.RequestAnalyticsDeploymentCost{
		{DeploymentID: "same-a", DeploymentCreatedAt: "2026-03-01T12:00:00Z", GuestCPUAvgMS: cpu(10), GuestCPUMeasuredRequests: 25},
		{DeploymentID: "same-b", DeploymentCreatedAt: "2026-03-01T12:00:00Z", GuestCPUAvgMS: cpu(20), GuestCPUMeasuredRequests: 25},
		{DeploymentID: "later", DeploymentCreatedAt: "2026-03-02T12:00:00Z", GuestCPUAvgMS: cpu(30), GuestCPUMeasuredRequests: 25},
	}

	annotateDeploymentCPURegressions(deployments)

	for _, deployment := range deployments {
		if deployment.GuestCPUChangePct != nil || deployment.GuestCPURegression {
			t.Fatalf("ambiguous chronology produced a CPU comparison: %+v", deployment)
		}
	}
}
