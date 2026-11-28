package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAlertThresholdUnreachable(t *testing.T) {
	for _, tc := range []struct {
		metric, comparison string
		threshold          float64
		never              bool
	}{
		{"error_rate_pct", "gt", 500, true},
		{"error_rate_pct", "gt", 100, true},
		{"error_rate_pct", "gte", 100, false},
		{"error_rate_pct", "gt", 99.5, false},
		{"latency_p95_ms", "lt", -1, true},
		{"latency_p95_ms", "lt", 0, true},
		{"latency_p95_ms", "lte", 0, false},
		{"latency_p95_ms", "gt", 100000, false},
		{"request_count", "lt", 1, false},
		{"slo_budget_remaining_pct", "lt", -10, false},
		{"slo_budget_remaining_pct", "gt", 150, false},
		{"error_rate_pct", "above_baseline", 500, false},
	} {
		if got := api.AlertThresholdUnreachable(tc.metric, tc.comparison, tc.threshold) != ""; got != tc.never {
			t.Errorf("%s %s %g: never-fires=%v, want %v", tc.metric, tc.comparison, tc.threshold, got, tc.never)
		}
	}
}
