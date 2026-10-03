package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-464: independent intent, optimistic revisions, entitlement and unknown telemetry.
func TestProductionRouteMonitorMemConfigurationAndWorker(t *testing.T) {
	s := state.NewMemStore()
	a, app, _, _ := healthFixture(t, s)
	c, err := s.GetRouteMonitor(t.Context(), a.ID, app.ID)
	if err != nil || c.Enabled || c.Revision != 0 || routemonitor.ValidateConfig(c) != nil {
		t.Fatalf("default %+v %v", c, err)
	}
	budget := int64(100)
	req := api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: new(int64), Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget, MaxP95MS: 300}}}
	c, err = s.SetRouteMonitor(t.Context(), a.ID, app.ID, req)
	if err != nil || c.Revision != 1 {
		t.Fatal(err)
	}
	*c.Routes[0].Max5xxRateBPS = 999
	req.ExpectedRevision = &c.Revision
	c, err = s.SetRouteMonitor(t.Context(), a.ID, app.ID, req)
	if err != nil || c.Revision != 1 || *c.Routes[0].Max5xxRateBPS != 100 {
		t.Fatal("no-op or defensive copy failed")
	}
	req.ExpectedRevision = new(int64)
	if _, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, req); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatal("stale revision accepted")
	}
	if _, err := s.GetRouteMonitor(t.Context(), uuid.NewString(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("ownership leak")
	}
	r, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
	if err != nil || r.Status != "unknown" || routemonitor.ValidateReport(r) != nil {
		t.Fatal("memory telemetry must stay unknown")
	}
	due, err := s.ListDueRouteMonitors(t.Context())
	if err != nil || len(due) != 1 {
		t.Fatal(err)
	}
	done, err := s.EvaluateRouteMonitor(t.Context(), a.ID, app.ID)
	if err != nil || !done {
		t.Fatal(err)
	}
	if done, err := s.EvaluateRouteMonitor(t.Context(), a.ID, app.ID); err != nil || done {
		t.Fatal("retry scheduled duplicate check")
	}
	page, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if err != nil || len(page.Incidents) != 0 {
		t.Fatal("unknown opened an incident")
	}
	if _, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unknown cursor accepted")
	}
	if _, err := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("missing incident accepted")
	}
	req.ExpectedRevision = &c.Revision
	req.Enabled = false
	c, err = s.SetRouteMonitor(t.Context(), a.ID, app.ID, req)
	if err != nil || c.Revision != 2 {
		t.Fatal(err)
	}
	if c, err := s.GetRouteHealthGate(t.Context(), a.ID, app.ID); err != nil || len(c.Routes) != 0 {
		t.Fatal("monitor changed canary intent")
	}
}
