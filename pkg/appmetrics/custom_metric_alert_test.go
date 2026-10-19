package appmetrics_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

func TestFetchCustomMetricAlert(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		value      float64
		err        error
		wantQuery  string
		wantValue  float64
		wantSource string
	}{
		{"gauge averages the window", appmetrics.CustomMetricKindGauge, 42, nil,
			`max(avg_over_time(gregale_app_custom_metric{app="app-1",name="orders_pending"}[15m])) or vector(-1)`, 42, appmetrics.SourcePrometheus},
		{"counter is a rate", appmetrics.CustomMetricKindCounter, 0.5, nil,
			`max(rate(gregale_app_custom_metric{app="app-1",name="orders_pending"}[15m])) or vector(-1)`, 0.5, appmetrics.SourcePrometheus},
		{"no samples is insufficient", appmetrics.CustomMetricKindGauge, -1, nil, "", 0, appmetrics.SourceInsufficientPrefix},
		{"query failure is degraded", appmetrics.CustomMetricKindGauge, 0, errors.New("prometheus unavailable"), "", 0, appmetrics.SourceDegradedPrefix},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			stub := &stubPromQL{fn: func(q string) (float64, error) { seen = q; return tt.value, tt.err }}
			got, source := appmetrics.FetchCustomMetricAlert(context.Background(), stub, nil, "app-1", "orders_pending", tt.kind, "15m")
			if tt.wantQuery != "" && seen != tt.wantQuery {
				t.Fatalf("query = %s, want %s", seen, tt.wantQuery)
			}
			if got != tt.wantValue || !strings.HasPrefix(source, tt.wantSource) {
				t.Fatalf("got (%v, %q), want (%v, %q…)", got, source, tt.wantValue, tt.wantSource)
			}
		})
	}
}

func TestFetchCustomMetricAlertRejectsBadInput(t *testing.T) {
	stub := &stubPromQL{fn: func(string) (float64, error) { t.Fatal("must not query"); return 0, nil }}
	for _, tc := range []struct{ app, name, rng string }{
		{"app-1", `x"}`, "5m"},
		{"app-1", "orders", "2h"},
	} {
		if _, source := appmetrics.FetchCustomMetricAlert(context.Background(), stub, nil, tc.app, tc.name, appmetrics.CustomMetricKindGauge, tc.rng); !appmetrics.IsDegradedSource(source) {
			t.Fatalf("%+v: source = %q, want degraded", tc, source)
		}
	}
	if _, source := appmetrics.FetchCustomMetricAlert(context.Background(), nil, nil, "app-1", "orders", appmetrics.CustomMetricKindGauge, "5m"); !appmetrics.IsDegradedSource(source) {
		t.Fatalf("nil fetcher source = %q, want degraded", source)
	}
}
