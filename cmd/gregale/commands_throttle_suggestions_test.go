package main_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// A paid app with route metrics switched off was told it was on the Free plan.
// The hint must name the setting and how to turn it on instead.
func TestCmdThrottleSuggestions_RouteMetricsOffNamesTheSetting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/gateway/throttle-suggestions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"app_id":"app-1","range":"1h","source":"prometheus","route_metrics_disabled":true,"plan_ceiling_rps":500,"plan_ceiling_burst":2000,"multiplier":1.5,"suggestions":[]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOutput()
	t.Cleanup(main.ResetJSONOutput)

	stdout, restore := main.CaptureStdout(t)
	code := main.CmdThrottleSuggestions([]string{"gateway"})
	restore()
	out := stdout.String()
	if code != 0 {
		t.Fatalf("code = %d, output: %s", code, out)
	}
	if strings.Contains(out, "Free plan") {
		t.Errorf("a Scale app was told it is on the Free plan: %s", out)
	}
	if !strings.Contains(out, "app gateway --route-metrics") {
		t.Errorf("hint does not say how to turn route metrics on: %s", out)
	}
}
