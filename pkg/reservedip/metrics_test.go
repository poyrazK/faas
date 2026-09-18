package reservedip

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsObserveUsesBoundedLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, "schedd")
	m.Observe(ReconcileObservation{Region: "customer-controlled-region", Outcome: "ready", Duration: time.Second, Routes: 3, Moved: 1})
	m.Observe(ReconcileObservation{Region: "other-region", Outcome: "unknown", Duration: 2 * time.Second, Failed: 1, Contended: 1})

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range metrics {
		for _, label := range metric.Metric[0].Label {
			if label.GetName() == "region" {
				t.Fatalf("region leaked into metric labels: %s", metric.GetName())
			}
		}
	}
	text := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		text = append(text, metric.GetName())
	}
	if !strings.Contains(strings.Join(text, ","), "schedd_reserved_ip_reconcile_total") {
		t.Fatalf("reserved IP metric family missing: %v", text)
	}
}
