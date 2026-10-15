package hosting

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPublicationMetricsTrackBacklogRecoveryAndFreshness(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, "apid")
	if duplicate := NewMetrics(registry, "apid"); duplicate.pending != metrics.pending {
		t.Fatal("duplicate collector registration")
	}
	store := state.NewMemStore()
	_, site := routingSite(t, store, true)
	provider := &routeSink{failing: map[string]bool{site.ID: true}}
	at := time.Now().Add(10 * time.Second)
	r := Reconciler{Store: store, Artifacts: &routeArtifacts{}, LeasedRoutes: &routeSink{}, DurableRoutes: provider,
		AdmissionEnabled: func() bool { return false }, Metrics: metrics, Now: func() time.Time { return at }}
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("expected provider failure")
	}
	if testutil.ToFloat64(metrics.pending) != 1 || testutil.ToFloat64(metrics.available) != 1 || testutil.ToFloat64(metrics.oldest) < 9 {
		t.Fatal("publication backlog was not observed")
	}
	provider.failing[site.ID] = false
	at = at.Add(time.Minute)
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(metrics.pending) != 0 || testutil.ToFloat64(metrics.oldest) != 0 || testutil.ToFloat64(metrics.lastSuccess) != float64(at.Unix()) {
		t.Fatal("recovery metrics did not converge")
	}
	metrics.observeBacklog(state.HostingRouteBacklog{}, errors.New("database unavailable"), at.Add(time.Minute))
	if testutil.ToFloat64(metrics.available) != 0 || testutil.ToFloat64(metrics.observedAt) != float64(at.Unix()) {
		t.Fatal("stale backlog looks fresh during database outage")
	}
	metrics.observePublication("https://secret.example/site-id", time.Second)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "outcome" {
					t.Fatalf("unbounded label: %v", label)
				}
				if label.GetValue() == "https://secret.example/site-id" {
					t.Fatal("raw error leaked to metrics")
				}
			}
		}
	}
}
