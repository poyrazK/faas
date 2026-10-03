// adr: 463 — bounded health labels and collector heartbeat semantics.

package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHealthMetricsRetainCatalogAndHeartbeatOnFailedSweep(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewHealthMetrics(registry, "apid", HealthPolicy{Enabled: true, StaleAfter: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	summary := HealthCollectionSummary{Enabled: true, CompletedAt: now, StaleAfter: 5 * time.Minute, Counts: HealthCounts{Healthy: 2, Degraded: 1, Stale: 3}}
	metrics.ObserveSweep(summary, nil)
	summary.CompletedAt = now.Add(time.Minute)
	summary.Counts = HealthCounts{}
	metrics.ObserveSweep(summary, errors.New("catalog unavailable"))
	if testutil.ToFloat64(metrics.lastSweep) != 1000 || testutil.ToFloat64(metrics.databases.WithLabelValues("healthy")) != 2 || testutil.ToFloat64(metrics.sweeps.WithLabelValues("error")) != 1 {
		t.Fatal("failed sweep overwrote the successful catalog or heartbeat")
	}
	metrics.Observe(HealthCollectionObservation{Outcome: "healthy", Duration: time.Second})
	metrics.Observe(HealthCollectionObservation{Outcome: "degraded"})
	metrics.Observe(HealthCollectionObservation{Outcome: "private-provider-id"})
	if testutil.ToFloat64(metrics.checks.WithLabelValues("healthy")) != 1 || testutil.CollectAndCount(metrics.checks) != 2 {
		t.Fatal("health check outcome labels escaped the closed set")
	}
	metrics.ObserveSweep(HealthCollectionSummary{}, nil)
	if testutil.ToFloat64(metrics.enabled) != 0 || testutil.ToFloat64(metrics.sweeps.WithLabelValues("disabled")) != 1 {
		t.Fatal("disabled monitoring is indistinguishable from a stalled collector")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "status" && label.GetName() != "outcome" {
					t.Fatalf("unexpected health metric label: %s", label.GetName())
				}
			}
		}
	}
}
