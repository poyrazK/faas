package gateway

// adr: 1152

import (
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWakeAheadLearnerPublishesTopEdges(t *testing.T) {
	l, clock, sink := newTestLearner()
	train(l, clock, "api", api.ServiceWakeAheadMinWakes-1, map[string]bool{"auth": true, "billing": false})
	l.Predict("api")
	sink.mu.Lock()
	early := len(sink.edges) + len(sink.wakes)
	sink.mu.Unlock()
	if early != 0 {
		t.Fatalf("nothing may be published before %d wakes, got edges=%v wakes=%v", api.ServiceWakeAheadMinWakes, sink.edges, sink.wakes)
	}
	train(l, clock, "api", 1, map[string]bool{"auth": true, "billing": false})
	l.Predict("api")
	sink.mu.Lock()
	auth, billing, wakes := sink.edges["api>auth"], sink.edges["api>billing"], sink.wakes["api"]
	sink.mu.Unlock()
	if auth != [2]float64{1, 1} || billing != [2]float64{1, 0} || wakes != float64(api.ServiceWakeAheadMinWakes) {
		t.Fatalf("auth=%v billing=%v wakes=%v", auth, billing, wakes)
	}
	// Three targets called more often push billing out of the top edges.
	train(l, clock, "api", 30, map[string]bool{"auth": true, "queue": true, "search": true})
	l.Predict("api")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if _, ok := sink.edges["api>billing"]; ok || len(sink.edges) != api.ServiceWakeAheadMaxTargets {
		t.Fatalf("edges = %v, want the top %d without billing", sink.edges, api.ServiceWakeAheadMaxTargets)
	}
	if !slices.Contains(sink.gone, "api>billing") {
		t.Fatalf("billing must be withdrawn, gone=%v", sink.gone)
	}
}

func TestMetricsWakeAheadEdgeSeries(t *testing.T) {
	m := NewMetrics()
	caller, target := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	m.WakeAheadOutcome(caller, target, WakeAheadUsed)
	m.WakeAheadOutcome("api", "auth", WakeAheadUsed) // not app UUIDs: fleet counter only
	if got := testutil.ToFloat64(m.serviceWakeAheadEdgeTotal.WithLabelValues(caller, target, WakeAheadUsed)); got != 1 {
		t.Fatalf("edge counter = %v", got)
	}
	if got := testutil.ToFloat64(m.serviceWakeAhead.WithLabelValues(WakeAheadUsed)); got != 2 {
		t.Fatalf("fleet counter = %v", got)
	}
	m.WakeAheadEdge(caller, target, 0.8, 0.9)
	if got := testutil.ToFloat64(m.serviceWakeAheadEdgeShare.WithLabelValues(caller, target, "call")); got != 0.8 {
		t.Fatalf("call share = %v", got)
	}
	m.WakeAheadEdgeGone(caller, target)
	if n := testutil.CollectAndCount(m.serviceWakeAheadEdgeShare); n != 0 {
		t.Fatalf("withdrawn edge left %d series", n)
	}
}
