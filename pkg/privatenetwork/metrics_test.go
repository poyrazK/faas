package privatenetwork

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsObserveRecordsAttachmentAndNodeOutcomes(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "schedd")
	metrics.Observe(ReconcileObservation{
		Outcome:  "error",
		Duration: 250 * time.Millisecond,
		Nodes: []RouteNodeObservation{
			{NodeID: "node-a", Status: "ready"},
			{NodeID: "node-b", Status: "error"},
		},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, family := range families {
		seen[family.GetName()] = true
	}
	for _, name := range []string{
		"schedd_private_network_reconcile_total",
		"schedd_private_network_reconcile_duration_seconds",
		"schedd_private_network_route_nodes_total",
	} {
		if !seen[name] {
			t.Fatalf("metric family %q missing from registry", name)
		}
	}
}

func TestMetricsNilRegistererIsSafe(t *testing.T) {
	metrics := NewMetrics(nil, "")
	metrics.Observe(ReconcileObservation{Outcome: "ready", Duration: time.Second})
	metrics.ObserveDuration("pending", time.Second)
}
