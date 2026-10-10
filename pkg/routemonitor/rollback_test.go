package routemonitor

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 952
func TestDecideRollback(t *testing.T) {
	released := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	budget := int64(100)
	config := api.RouteMonitorConfig{Enabled: true, OnViolation: "rollback", Revision: 3}
	incident := func(mutate func(*api.RouteMonitorIncident)) api.RouteMonitorIncident {
		i := api.RouteMonitorIncident{
			ID: "i", DeploymentID: "current", Revision: 3, Status: "open", OpenedAt: released.Add(5 * time.Minute),
			Baseline: &api.RouteMonitorDeploymentBaseline{DeploymentID: "previous"},
			OpeningReport: api.RouteMonitorReport{Routes: []api.RouteMonitorFinding{
				{Route: api.RouteMonitorRoute{Method: "GET", Path: "/slow", MaxP95MS: 200}, LatencyStatus: "violated"},
				{Route: api.RouteMonitorRoute{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}, ErrorStatus: "violated"},
			}},
		}
		if mutate != nil {
			mutate(&i)
		}
		return i
	}
	cases := []struct {
		name   string
		config api.RouteMonitorConfig
		i      api.RouteMonitorIncident
		want   RollbackDecision
	}{
		{"eligible error budget", config, incident(nil), RollbackDecision{Eligible: true, Route: "POST /checkout", Target: "previous"}},
		{"report mode", api.RouteMonitorConfig{Enabled: true, Revision: 3}, incident(nil), RollbackDecision{}},
		{"stale revision", config, incident(func(i *api.RouteMonitorIncident) { i.Revision = 2 }), RollbackDecision{}},
		{"already decided", config, incident(func(i *api.RouteMonitorIncident) { i.Rollback = &api.RouteMonitorIncidentRollback{Status: "skipped"} }), RollbackDecision{}},
		{"recovered", config, incident(func(i *api.RouteMonitorIncident) { i.Status = "recovered" }), RollbackDecision{}},
		{"latency only", config, incident(func(i *api.RouteMonitorIncident) { i.OpeningReport.Routes = i.OpeningReport.Routes[:1] }), RollbackDecision{Skip: RollbackSkipLatencyOnly}},
		{"outside window", config, incident(func(i *api.RouteMonitorIncident) {
			i.OpenedAt = released.Add(api.RouteMonitorRollbackWindow + time.Second)
		}), RollbackDecision{Skip: RollbackSkipOutsideWindow, Route: "POST /checkout"}},
		{"window edge", config, incident(func(i *api.RouteMonitorIncident) { i.OpenedAt = released.Add(api.RouteMonitorRollbackWindow) }), RollbackDecision{Eligible: true, Route: "POST /checkout", Target: "previous"}},
		{"no baseline", config, incident(func(i *api.RouteMonitorIncident) { i.Baseline = nil }), RollbackDecision{Skip: RollbackSkipNoBaseline, Route: "POST /checkout"}},
		{"baseline is itself", config, incident(func(i *api.RouteMonitorIncident) { i.Baseline.DeploymentID = "current" }), RollbackDecision{Skip: RollbackSkipNoBaseline, Route: "POST /checkout"}},
	}
	for _, tc := range cases {
		if got := DecideRollback(tc.config, tc.i, released); got != tc.want {
			t.Errorf("%s: DecideRollback = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// adr: 952
func TestValidateOnViolation(t *testing.T) {
	zero, budget := int64(0), int64(100)
	request := func(onViolation string, routes ...api.RouteMonitorRoute) api.SetRouteMonitorRequest {
		return api.SetRouteMonitorRequest{Enabled: true, OnViolation: onViolation, ExpectedRevision: &zero, Routes: routes}
	}
	errorRoute := api.RouteMonitorRoute{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}
	latencyRoute := api.RouteMonitorRoute{Method: "GET", Path: "/slow", MaxP95MS: 300}
	for _, ok := range []api.SetRouteMonitorRequest{request("", errorRoute), request("report", latencyRoute), request("rollback", latencyRoute, errorRoute)} {
		if err := Validate(ok); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []api.SetRouteMonitorRequest{request("rollback", latencyRoute), request("abort", errorRoute)} {
		if Validate(bad) == nil {
			t.Errorf("Validate(%+v) accepted an invalid on_violation", bad)
		}
	}
}

func TestReleasedAtUsesLatestTrafficTransition(t *testing.T) {
	created := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	stage, completed := created.Add(time.Minute), created.Add(10*time.Minute)
	if got := ReleasedAt(created, &stage, &completed); !got.Equal(completed) {
		t.Fatalf("ReleasedAt = %s, want %s", got, completed)
	}
	if got := ReleasedAt(created, nil, nil); !got.Equal(created) {
		t.Fatalf("ReleasedAt without transitions = %s, want %s", got, created)
	}
}
