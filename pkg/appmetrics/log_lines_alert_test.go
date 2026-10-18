package appmetrics_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

func TestFetchAlertMetricLogLines(t *testing.T) {
	tests := []struct {
		metric    string
		wantQuery string
	}{
		{"log_error_lines", `sum(increase(vmmd_app_log_lines_total{app_id="app-1",level="error"}[15m])) or vector(0)`},
		{"log_warn_lines", `sum(increase(vmmd_app_log_lines_total{app_id="app-1",level="warn"}[15m])) or vector(0)`},
	}
	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			var seen string
			stub := &stubPromQL{fn: func(q string) (float64, error) { seen = q; return 41.6, nil }}
			got, source := appmetrics.FetchAlertMetric(context.Background(), stub, nil, "app-1", "15m", tt.metric)
			if seen != tt.wantQuery {
				t.Fatalf("query = %s, want %s", seen, tt.wantQuery)
			}
			// increase() extrapolates; a line count is a whole number,
			// normalised like request_count.
			if got != 41 || source != appmetrics.SourcePrometheus {
				t.Fatalf("got (%v, %q), want (41, prometheus)", got, source)
			}
		})
	}
}
