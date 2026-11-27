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

// `metrics` printed "Wake p95: 18000ms (fleet-wide)" from 4 fleet wakes
// while the status page withheld it below 20 (Hunt 12 H12-40).
func TestMetricsWithholdsThinFleetWakeP95(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	appP95 := 420.0
	tests := []struct {
		name       string
		samples    *int64
		appSamples *int64
		want       string
	}{
		{"four wakes", n(4), nil, "Wake p95:   — (fleet-wide; 4 wakes in the window, 20 needed)"},
		{"enough wakes", n(250), nil, "Wake p95:   18000ms (fleet-wide)"},
		{"count unknown (older apid)", nil, nil, "Wake p95:   18000ms (fleet-wide)"},
		// Observe audit 2026-11-21 (finding 6): the app's own wake p95.
		{"app's own wakes", n(250), n(40), "Wake p95:   420ms (this app, 40 wakes)"},
		{"too few app wakes falls back to the fleet", n(250), n(3), "Wake p95:   18000ms (fleet-wide)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/web/metrics" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{Range: "1h", Source: "prometheus", RequestCount: 23, WakeP95MS: 18000, WakeSamples: tt.samples, AppWakeP95MS: &appP95, AppWakeSamples: tt.appSamples})
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run([]string{"metrics", "web", "--range", "1h"})
			restore()
			if code != 0 || !strings.Contains(out.String(), tt.want) {
				t.Fatalf("exit %d, want %q in:\n%s", code, tt.want, out.String())
			}
		})
	}
}
