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

// The analytics header printed the API's nanosecond RFC 3339 bounds
// ("2026-10-22T06:25:57.396035705Z"); it now prints seconds in UTC
// (Hunt 12 H12-23).
func TestAnalyticsWindowPrintsSeconds(t *testing.T) {
	tests := []struct {
		name, from, until, want string
	}{
		{"nanoseconds", "2026-10-22T06:25:57.396035705Z", "2026-10-22T12:25:57.396035705Z", "Window:     6h (2026-10-22 06:25:57 UTC to 2026-10-22 12:25:57 UTC)"},
		{"offset", "2026-10-22T08:25:57+02:00", "2026-10-22T14:25:57+02:00", "Window:     6h (2026-10-22 06:25:57 UTC to 2026-10-22 12:25:57 UTC)"},
		{"unparseable", "yesterday", "now", "Window:     6h (yesterday to now)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(api.RequestAnalyticsResponse{Slug: "web", Since: "6h", From: tt.from, Until: tt.until, GroupBy: "route"})
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run([]string{"analytics", "web", "--since", "6h"})
			restore()
			if code != 0 || !strings.Contains(out.String(), tt.want) {
				t.Fatalf("exit %d, output missing %q:\n%s", code, tt.want, out.String())
			}
		})
	}
}
