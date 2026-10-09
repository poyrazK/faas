package main

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidateCustomMetricAlert(t *testing.T) {
	webhook, rollback := "webhook", "rollback"
	tests := []struct {
		name     string
		metric   string
		metricNm string
		window   string
		action   *string
		enabled  bool
		want     int // 0 = valid
	}{
		{"valid gauge rule", "custom_metric", "orders_pending", "15m", nil, true, 0},
		{"explicit webhook", "custom_metric", "orders_pending", "24h", &webhook, true, 0},
		{"other metric without name", "error_rate_pct", "", "7d", nil, false, 0},
		{"history disabled", "custom_metric", "orders_pending", "15m", nil, false, http.StatusServiceUnavailable},
		{"missing name", "custom_metric", "", "15m", nil, true, http.StatusBadRequest},
		{"bad name", "custom_metric", "Orders", "15m", nil, true, http.StatusBadRequest},
		{"rollback action", "custom_metric", "orders_pending", "15m", &rollback, true, http.StatusBadRequest},
		{"window too long", "custom_metric", "orders_pending", "7d", nil, true, http.StatusBadRequest},
		{"name on other metric", "error_rate_pct", "orders_pending", "5m", nil, true, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validateCustomMetricAlert(tt.metric, tt.metricNm, tt.window, tt.action, tt.enabled)
			switch {
			case tt.want == 0 && p != nil:
				t.Fatalf("got %+v, want valid", p)
			case tt.want != 0 && (p == nil || p.Status != tt.want):
				t.Fatalf("got %+v, want status %d", p, tt.want)
			}
		})
	}
}

func TestCustomMetricAlertFamilyCannotChange(t *testing.T) {
	if alertRuleFamily(state.AlertMetricCustomMetric) == alertRuleFamily(state.AlertMetricErrorRate) {
		t.Fatal("custom_metric shares a family with error_rate_pct; a PATCH could orphan custom_metric_name")
	}
	merged := state.AlertRule{Metric: state.AlertMetricCustomMetric, WindowSpec: state.AlertWindowSpec("7d")}
	if p := validateCustomMetricAlertWindow(string(merged.WindowSpec)); p == nil || p.Code != api.CodeAlertRuleInvalid {
		t.Fatalf("7d window = %+v, want alert_rule_invalid", p)
	}
}
