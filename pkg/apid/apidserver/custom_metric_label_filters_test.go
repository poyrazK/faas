package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261108160000 slice C2 — only a custom_metric rule takes a label
// filter, and it has the shape a pushed label set has.
func TestValidateCustomMetricAlertLabels(t *testing.T) {
	tests := []struct {
		name   string
		metric string
		labels map[string]string
		ok     bool
	}{
		{"no labels", "error_rate_pct", nil, true},
		{"custom metric with labels", "custom_metric", map[string]string{"region": "eu"}, true},
		{"labels on another metric", "error_rate_pct", map[string]string{"region": "eu"}, false},
		{"bad key", "custom_metric", map[string]string{"Region": "eu"}, false},
		{"too many labels", "custom_metric", map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}, false},
		{"value too long", "custom_metric", map[string]string{"region": strings.Repeat("x", 129)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if p := apidserver.ValidateCustomMetricAlertLabels(tt.metric, tt.labels); (p == nil) != tt.ok {
				t.Fatalf("problem = %+v, want ok=%v", p, tt.ok)
			}
		})
	}
}
