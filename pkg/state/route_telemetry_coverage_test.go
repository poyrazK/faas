package state_test

// adr: 20261028110044

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
)

// A gateway that may have lost telemetry in the evidence windows makes the
// canary guard's evidence unavailable: the advance waits, the hold says the
// evidence was lost, and no customer-visible hold starts. A continuously
// healthy gateway, or an inactive node, does not.
func TestRouteEvidenceUnavailableWhileGatewayLostTelemetry(t *testing.T) {
	pool, s, a, app, d, seed := healthNotificationPG(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	seed(100, 120)
	exec(`INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES('coverage-gw','unix:///run/test.sock',2,1024,10,512)`)
	// Mature heartbeat history is fixture data; runtime writers cannot backdate it.
	exec(`INSERT INTO request_telemetry_coverage(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
		VALUES('coverage-gw',gen_random_uuid(),1,true,10000,0,0,clock_timestamp(),clock_timestamp(),clock_timestamp()-interval '2 hours')`)
	report := func() api.RouteHealthReport {
		t.Helper()
		r, err := s.GetRouteHealthReport(ctx, a.ID, app.ID, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := report(); r.Status != "healthy" || routehealth.EvidenceLost(r) {
		t.Fatalf("healthy gateway: status %s (%s), lost %v", r.Status, r.Reason, routehealth.EvidenceLost(r))
	}
	for _, tc := range []struct{ name, sql string }{
		{"restart inside the windows", "UPDATE request_telemetry_coverage SET healthy_since=clock_timestamp()"},
		{"no report since the windows ended", "UPDATE request_telemetry_coverage SET healthy_since=clock_timestamp()-interval '2 hours',source_at=clock_timestamp()-interval '10 minutes'"},
		{"sampled", "UPDATE request_telemetry_coverage SET source_at=clock_timestamp(),sampling_basis_points=5000,healthy_since=NULL"},
		{"disabled", "UPDATE request_telemetry_coverage SET sampling_basis_points=10000,enabled=false,healthy_since=NULL"},
	} {
		exec(tc.sql)
		r := report()
		if r.Status != "unknown" || !routehealth.EvidenceLost(r) {
			t.Fatalf("%s: status %s (%s), lost %v; want unavailable evidence", tc.name, r.Status, r.Reason, routehealth.EvidenceLost(r))
		}
	}
	var decision api.RouteHealthDecision
	_, _, err := s.AdvanceCanary(ctx, d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision})
	var blocked *state.RouteHealthBlockedError
	if !errors.As(err, &blocked) || !blocked.EvidenceLost {
		t.Fatalf("advance on lost evidence: %v, want a hold that says the evidence was lost", err)
	}
	if _, total, err := s.RouteHealthHoldsSince(ctx, app.ID, time.Now().Add(-time.Hour), 5); err != nil || total != 0 {
		t.Fatalf("lost evidence started a customer-visible hold: total %d err %v", total, err)
	}
	exec(`UPDATE compute_nodes SET active=false WHERE name='coverage-gw'`)
	if r := report(); r.Status != "healthy" || routehealth.EvidenceLost(r) {
		t.Fatalf("inactive node counted: status %s (%s)", r.Status, r.Reason)
	}
}

// The production monitor reports a gateway's lost telemetry as unavailable
// evidence: no incident opens and no healthy baseline is recorded from it.
func TestProductionRouteMonitorUnavailableWhileGatewayLostTelemetry(t *testing.T) {
	pool, s, a, app, _, candidate := productionMonitorPG(t)
	productionMonitorTraffic(t, pool, a, app, candidate.ID, "POST", "/checkout", 500, 100, 600, false)
	productionMonitorExec(t, pool, `INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES('coverage-gw','unix:///run/test.sock',2,1024,10,512)`)
	productionMonitorExec(t, pool, `INSERT INTO request_telemetry_coverage(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
		VALUES('coverage-gw',gen_random_uuid(),1,true,10000,0,0,clock_timestamp(),clock_timestamp(),clock_timestamp())`)
	r, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
	if err != nil || r.Status != "unknown" || r.Reason != "telemetry_unavailable" {
		t.Fatalf("monitor on lost telemetry: status %s reason %s err %v", r.Status, r.Reason, err)
	}
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	page, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if err != nil || len(page.Incidents) != 0 {
		t.Fatalf("incident opened on lost telemetry: %+v %v", page.Incidents, err)
	}
}
