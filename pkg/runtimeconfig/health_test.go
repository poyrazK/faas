package runtimeconfig

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHealthPolicyRequiresTrafficAndRejectsRegressions(t *testing.T) {
	policy := HealthPolicy{MinRequests: 10, MaxErrorRatePct: 2, MaxP95LatencyMs: 500}
	cases := []struct {
		name    string
		snap    HealthSnapshot
		wantErr string
	}{
		{"insufficient traffic", HealthSnapshot{Requests: 9}, "insufficient traffic"},
		{"error rate", HealthSnapshot{Requests: 10, ErrorRatePct: 2.1, P95LatencyMs: 100}, "error rate"},
		{"latency", HealthSnapshot{Requests: 10, ErrorRatePct: 1, P95LatencyMs: 501}, "latency"},
		{"healthy", HealthSnapshot{Requests: 10, ErrorRatePct: 1, P95LatencyMs: 500}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.Evaluate(tc.snap)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Evaluate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Evaluate() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

type fakePromQL struct {
	values  []float64
	queries []string
}

func (f *fakePromQL) QueryScalar(_ context.Context, query string) (float64, error) {
	f.queries = append(f.queries, query)
	value := f.values[0]
	f.values = f.values[1:]
	return value, nil
}

func TestPrometheusHealthProviderQueriesStandardSignals(t *testing.T) {
	client := &fakePromQL{values: []float64{100, 1.5, 250}}
	provider := PrometheusHealthProvider{
		Client: client,
		Policy: HealthPolicy{Window: 90 * time.Second},
		Now:    func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
	}
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Requests != 100 || snapshot.ErrorRatePct != 1.5 || snapshot.P95LatencyMs != 250 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(client.queries) != 3 || !strings.Contains(client.queries[0], "[90s]") {
		t.Fatalf("queries = %#v", client.queries)
	}
}
