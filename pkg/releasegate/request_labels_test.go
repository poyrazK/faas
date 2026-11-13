package releasegate

import (
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261108160000 slice C2 — custom_metric and metric_change gates may
// select one labelled series; other kinds refuse labels.
func TestFromRequestLabels(t *testing.T) {
	threshold := 5.0
	eu := map[string]string{"region": "eu"}
	tests := []struct {
		name string
		req  api.CreateReleaseGateRequest
		ok   bool
	}{
		{"custom_metric with labels", api.CreateReleaseGateRequest{Name: "orders", Kind: KindCustomMetric, Metric: "orders", Comparison: "below", Threshold: &threshold, Labels: eu}, true},
		{"metric_change with labels", api.CreateReleaseGateRequest{Name: "orders", Kind: KindMetricChange, Metric: "orders", Comparison: "below", MaxChangePercent: 30, Labels: eu}, true},
		{"ai gate refuses labels", api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricErrorRate, Labels: eu}, false},
		{"bad label key", api.CreateReleaseGateRequest{Name: "orders", Kind: KindCustomMetric, Metric: "orders", Comparison: "below", Threshold: &threshold, Labels: map[string]string{"Bad-Key": "x"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, p := FromRequest(tt.req)
			if (p == nil) != tt.ok {
				t.Fatalf("problem = %+v, want ok=%v", p, tt.ok)
			}
			if tt.ok && !maps.Equal(spec.MetricLabels, tt.req.Labels) {
				t.Fatalf("labels = %v, want %v", spec.MetricLabels, tt.req.Labels)
			}
		})
	}
}
