package appmetrics_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// adr: 20261104120000 — a stage's alert metrics read only the stage's
// series.
func TestFetchEnvironmentAlertMetric(t *testing.T) {
	for _, metric := range appmetrics.EnvironmentAlertMetrics {
		t.Run(metric, func(t *testing.T) {
			var queries []string
			stub := &stubPromQL{fn: func(q string) (float64, error) {
				queries = append(queries, q)
				return 3, nil
			}}
			_, source := appmetrics.FetchEnvironmentAlertMetric(context.Background(), stub, nil, "app-1", "env-1", "5m", metric)
			if source != appmetrics.SourcePrometheus || len(queries) != 1 {
				t.Fatalf("source = %q, queries = %d", source, len(queries))
			}
			q := queries[0]
			if !strings.Contains(q, `app="app-1",environment="env-1"`) || !strings.Contains(q, "gateway_environment_") ||
				strings.Contains(q, "gateway_request_duration_seconds") || strings.Contains(q, "gateway_cold_boot_total") ||
				strings.Contains(q, "gateway_capacity_refusals_total") {
				t.Fatalf("query %q reads more than the stage", q)
			}
		})
	}
	stub := &stubPromQL{fn: func(string) (float64, error) { return 0, nil }}
	for _, tc := range []struct{ app, env, metric string }{
		{"app-1", "env-1", "queue_depth"},
		{"app-1", "", "request_count"},
		{"app-1", `env"`, "request_count"},
	} {
		if _, source := appmetrics.FetchEnvironmentAlertMetric(context.Background(), stub, nil, tc.app, tc.env, "5m", tc.metric); !appmetrics.IsDegradedSource(source) {
			t.Fatalf("%+v: source = %q, want degraded", tc, source)
		}
	}
}

// adr: 20261104120000 — a production rule's request metrics leave out the
// requests its stages' hosts served.
func TestFetchAlertMetricLeavesOutStages(t *testing.T) {
	for _, metric := range appmetrics.EnvironmentAlertMetrics {
		t.Run(metric, func(t *testing.T) {
			var queries []string
			stub := &stubPromQL{fn: func(q string) (float64, error) {
				queries = append(queries, q)
				return 3, nil
			}}
			if _, source := appmetrics.FetchAlertMetric(context.Background(), stub, nil, "app-1", "5m", metric); source != appmetrics.SourcePrometheus {
				t.Fatalf("source = %q", source)
			}
			if len(queries) != 1 || !strings.Contains(queries[0], `- (sum`) || !strings.Contains(queries[0], `gateway_environment_`) {
				t.Fatalf("query %q does not subtract the stages' series", queries)
			}
		})
	}
}
