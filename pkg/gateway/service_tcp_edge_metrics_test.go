// adr: 1131
package gateway

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1131: authorized private TCP sessions are counted on their caller ->
// target edge, with the sessions that had to wake the target. Refusals before
// authorization and the caller's own plan session limit are not edge traffic.
func TestServiceTCPProxyCountsEdgeSessions(t *testing.T) {
	callerID, targetID := uuid.NewString(), uuid.NewString()
	h := newTCPTestHarness(t)
	h.caller = callerID
	h.targets[netip.MustParseAddr("198.19.0.7")] = ServiceTCPTarget{
		AppID: targetID, AccountID: "acct", Plan: api.PlanPro, TCPPorts: []int{5432}, IdleTimeout: time.Minute,
	}
	m := NewMetrics()
	h.proxy.cfg.Services.metrics = m
	h.onWake = func() {
		h.provider.set(targetID, ServiceEndpoint{InstanceID: "i1", NodeID: "n1", Port: 8080})
	}
	sessions := func(outcome string) float64 {
		return testutil.ToFloat64(m.serviceTCPEdgeSessions.WithLabelValues(callerID, targetID, outcome))
	}
	wakes := func() float64 {
		return testutil.ToFloat64(m.serviceTCPEdgeWakes.WithLabelValues(callerID, targetID))
	}

	if outcome, reason := h.serve(t, "caller", "198.19.0.7:5432"); outcome != "success" {
		t.Fatalf("parked target: outcome=%q reason=%q, want success", outcome, reason)
	}
	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "" {
		t.Fatalf("warm target: reason=%q", reason)
	}
	if sessions("success") != 2 || wakes() != 1 {
		t.Fatalf("sessions=%g wakes=%g, want 2 sessions and 1 wake (only the first restored the target)", sessions("success"), wakes())
	}

	h.dead["i1"] = true
	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason == "" {
		t.Fatal("unreachable replica: session succeeded")
	}
	if sessions("error") != 1 {
		t.Fatalf("error sessions = %g, want 1 for an authorized session that failed", sessions("error"))
	}

	if _, reason := h.serve(t, "caller", "198.19.0.7:22"); reason != "undeclared_port" {
		t.Fatalf("undeclared port: reason=%q", reason)
	}
	h.proxy.cfg.SessionsPerAccount = func(api.Plan) int { return 0 }
	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "account_limit" {
		t.Fatalf("session limit: reason=%q", reason)
	}
	if total := sessions("success") + sessions("error"); total != 3 {
		t.Fatalf("edge sessions = %g, want 3: refusals before authorization and plan-limit refusals are not counted", total)
	}
}

func TestObserveServiceTCPEdgeDropsUntrustedLabels(t *testing.T) {
	m := NewMetrics()
	m.ObserveServiceTCPEdge("api", uuid.NewString(), false, true)
	m.ObserveServiceTCPEdge(uuid.NewString(), "db", true, false)
	if n := testutil.CollectAndCount(m.serviceTCPEdgeSessions); n != 0 {
		t.Fatalf("session series = %d, want 0 for non-UUID app labels", n)
	}
	if n := testutil.CollectAndCount(m.serviceTCPEdgeWakes); n != 0 {
		t.Fatalf("wake series = %d, want 0 for non-UUID app labels", n)
	}
	var nilMetrics *Metrics
	nilMetrics.ObserveServiceTCPEdge(uuid.NewString(), uuid.NewString(), false, true)
}
