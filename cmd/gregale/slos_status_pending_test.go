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

// `slos status` on a new SLO printed "no traffic (0 of 0 requests)" next to
// a live burn rate until the first hourly rollup closed (Hunt 12 H12-22).
func TestSLOsStatusPendingWindow(t *testing.T) {
	burn := 9.3
	tests := []struct {
		name   string
		status api.SLOStatus
		want   []string
		reject []string
	}{
		{
			name:   "creation hour open",
			status: api.SLOStatus{WindowStart: "2026-10-22T12:00:00Z", BurnRate1h: &burn},
			want:   []string{"Attained:          pending: first hour closes 13:00 UTC", "Budget remaining:  pending", "9.30× (1h)"},
			reject: []string{"no traffic"},
		},
		{
			name:   "closed hour without requests",
			status: api.SLOStatus{WindowStart: "2026-10-22T12:00:00Z", HoursExpected: 1, HoursRecorded: 1},
			want:   []string{"Attained:          no traffic (0 of 0 requests)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/web/slos/slo-1" {
					http.NotFound(w, r)
					return
				}
				st := tt.status
				_ = json.NewEncoder(w).Encode(api.SLOResponse{ID: "slo-1", Name: "avail", SLI: "availability", ObjectivePct: 99.5, WindowDays: 7, Status: &st})
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run([]string{"slos", "status", "--app", "web", "slo-1"})
			restore()
			got := out.String()
			if code != 0 {
				t.Fatalf("exit %d: %s", code, got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
			for _, r := range tt.reject {
				if strings.Contains(got, r) {
					t.Errorf("output contains %q:\n%s", r, got)
				}
			}
		})
	}
}
