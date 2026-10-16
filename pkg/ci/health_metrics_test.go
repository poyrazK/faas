package ci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type healthMetricsReader struct {
	calls int
	fail  bool
}

func (s *healthMetricsReader) CIFleetHealth(context.Context) (Health, error) {
	s.calls++
	if s.fail {
		return Health{}, errors.New("sensitive provider detail")
	}
	limit, headroom := int64(16), int64(15)
	return Health{Queues: []QueueHealth{{Name: "executions", Pending: 1, Due: 1, OldestItemAgeSeconds: 300}}, Quotas: []QuotaHealth{{Resource: "active_executions", Used: 1, Limit: &limit, Headroom: &headroom}}, Workers: summarizeWorkers(time.Now(), nil)}, nil
}
func TestCIHealthMetricsBoundLabelsFailureAndDormancy(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	reader := &healthMetricsReader{}
	enabled := false
	metrics := NewHealthMetrics(t.Context(), registry, "apid", reader, func() bool { return enabled }, func() map[WorkerKind]bool { return (Config{}).WorkerConfiguration("apid") })
	if NewHealthMetrics(t.Context(), registry, "apid", reader, func() bool { return enabled }, func() map[WorkerKind]bool { return (Config{}).WorkerConfiguration("apid") }) != metrics {
		t.Fatal("duplicate metrics collector registered")
	}
	families, err := registry.Gather()
	if err != nil || reader.calls != 0 || len(families) != 2 || families[0].GetName() != "apid_ci_health_enabled" || families[0].Metric[0].Gauge.GetValue() != 0 {
		t.Fatal("all-off metrics queried DB or invented health")
	}
	enabled = true
	families, err = registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, family := range families {
		names[family.GetName()] = true
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "queue", "condition", "worker", "resource":
				default:
					t.Fatalf("unbounded label: %s", label.GetName())
				}
			}
		}
	}
	for _, name := range []string{"apid_ci_health_up", "apid_ci_queue_items", "apid_ci_resource_headroom", "apid_ci_worker_replicas", "apid_ci_worker_enabled"} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}
	reader.fail = true
	families, err = registry.Gather()
	if err != nil || len(families) != 3 {
		t.Fatal("failed read exported healthy zero counts")
	}
	for _, family := range families {
		if family.GetName() == "apid_ci_health_up" && family.Metric[0].Gauge.GetValue() != 0 {
			t.Fatal("failed read stayed up")
		}
		if strings.Contains(family.String(), "sensitive") {
			t.Fatal("provider detail leaked to metrics")
		}
	}
}
