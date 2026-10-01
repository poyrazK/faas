package udpd

import (
	"testing"
)

func assertMetric(t *testing.T, m *Metrics, name string, labels map[string]string, want float64) {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != len(labels) {
				continue
			}
			match := true
			for _, label := range metric.Label {
				if labels[label.GetName()] != label.GetValue() {
					match = false
				}
			}
			if !match {
				continue
			}
			var got float64
			if metric.Counter != nil {
				got = metric.Counter.GetValue()
			} else if metric.Gauge != nil {
				got = metric.Gauge.GetValue()
			} else {
				t.Fatal("unexpected metric type")
			}
			if got != want {
				t.Errorf("%s%v=%v, want %v", name, labels, got, want)
			}
			return
		}
	}
	t.Fatalf("metric %s%v missing", name, labels)
}
func TestUDPMetricsLabelsAreBounded(t *testing.T) {
	m := NewMetrics(nil, "test")
	start := m.beginPeer()
	m.endPeer(start, "arbitrary-account-secret")
	m.drop("arbitrary-peer-address")
	assertMetric(t, m, "test_udp_peers_completed_total", map[string]string{"outcome": "other"}, 1)
	assertMetric(t, m, "test_udp_datagrams_dropped_total", map[string]string{"reason": "other"}, 1)
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "direction", "reason", "outcome":
				default:
					t.Errorf("unbounded label %q", label.GetName())
				}
				if label.GetValue() == "arbitrary-account-secret" || label.GetValue() == "arbitrary-peer-address" {
					t.Error("wire data leaked into metrics")
				}
			}
		}
	}
}
