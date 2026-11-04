package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A check whose only run failed printed "p95 0 ms": the p95 covers
// successful runs and is 0 without any (Hunt 12 H12-25).
func TestSyntheticsStatusLatencyWithoutSuccessfulRuns(t *testing.T) {
	tests := []struct {
		name   string
		p95    float64
		ok     bool
		want   string
		reject string
	}{
		{"only failures", 0, false, "Latency:  — (no successful runs in 24h)", "p95 0 ms"},
		{"successful runs", 214, true, "Latency:  p95 214 ms over 24h of successful runs, including wakes", "no successful runs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(api.SyntheticCheckResponse{
					ID: "chk-1", Name: "missing", Method: "GET", URL: "https://web.gregale.dev/missing", IntervalSeconds: 300, Enabled: true,
					Results: &api.SyntheticCheckResults{Runs24h: 1, P95LatencyMS24h: tt.p95, Recent: []api.SyntheticCheckRun{{StartedAt: "2026-10-22T12:26:06Z", OK: tt.ok, StatusCode: 404, LatencyMS: 127}}},
				})
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run([]string{"synthetics", "status", "--app", "web", "chk-1"})
			restore()
			got := out.String()
			if code != 0 || !strings.Contains(got, tt.want) || strings.Contains(got, tt.reject) {
				t.Fatalf("exit %d, want %q and not %q:\n%s", code, tt.want, tt.reject, got)
			}
		})
	}
}
