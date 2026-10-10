package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func pooledRouteHealthReport(t *testing.T) api.RouteHealthReport {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	anchor := now.Add(-12 * time.Minute)
	pooled, ok := routehealth.PooledWindows(&anchor, now)
	if !ok {
		t.Fatal("expected pooled windows")
	}
	f := api.RouteHealthFinding{Method: "POST", Path: "/refund", Windows: routehealth.Windows(now), PooledWindows: pooled}
	for i := range f.Windows {
		f.Windows[i].Candidate, f.Windows[i].Stable = api.RouteHealthCounts{Requests: 5}, api.RouteHealthCounts{Requests: 100}
	}
	for i := range f.PooledWindows {
		f.PooledWindows[i].Candidate, f.PooledWindows[i].Stable = api.RouteHealthCounts{Requests: 40}, api.RouteHealthCounts{Requests: 500}
	}
	r := api.RouteHealthReport{
		AppID: uuid.NewString(), DeploymentID: uuid.NewString(), StableDeploymentID: uuid.NewString(), Mode: "report", OnRegression: "hold",
		Revision: 1, CheckedAt: now, ObservationAnchor: &anchor, Routes: []api.RouteHealthFinding{f},
	}
	routehealth.Evaluate(&r, &anchor, "")
	return r
}

// adr: 846
func TestValidateRouteHealthReportAcceptsPooledEvidence(t *testing.T) {
	r := pooledRouteHealthReport(t)
	if r.Routes[0].EvidenceWindow != "pooled" || r.Status != "healthy" {
		t.Fatalf("fixture is not pooled: %+v", r.Routes[0])
	}
	if err := validateRouteHealthReport(r, r.DeploymentID); err != nil {
		t.Fatalf("pooled report rejected: %v", err)
	}
	shifted := pooledRouteHealthReport(t)
	shifted.Routes[0].PooledWindows[0].Start = shifted.Routes[0].PooledWindows[0].Start.Add(-time.Minute)
	if validateRouteHealthReport(shifted, shifted.DeploymentID) == nil {
		t.Fatal("shifted pooled windows accepted")
	}
	unlabeled := pooledRouteHealthReport(t)
	unlabeled.Routes[0].EvidenceWindow = ""
	if validateRouteHealthReport(unlabeled, unlabeled.DeploymentID) == nil {
		t.Fatal("pooled verdict without its label accepted")
	}
}
