package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294, h14-py with route monitoring off and no saved route
// requirements (Hunt 14 H14-60): `routes monitor incidents` printed nothing,
// `routes monitor report` printed "Deployment:  ()", and `routes
// requirements get` answered "Not found: app, saved route requirements or
// deployment".
func TestRouteCommandsSayWhatIsMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/h14-py/route-monitor/incidents":
			_, _ = w.Write([]byte(`{"app_id":"6f3c1a9e-1b2c-4d5e-8f90-123456789abc","incidents":[]}`))
		case "/v1/apps/h14-py":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"h14-py"}`))
		case "/v1/apps/h14-py/route-requirements":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"title":"Not found","status":404,"detail":"app, saved route requirements or deployment"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	main.ResetJSONOut(t)
	run := func(args ...string) (int, string, string) {
		var out bytes.Buffer
		previous := *main.OsStdout
		*main.OsStdout = &out
		stderr, restore := main.CaptureStderr(t)
		code := main.Run(args)
		restore()
		*main.OsStdout = previous
		return code, out.String(), stderr.String()
	}
	if code, out, errOut := run("routes", "monitor", "incidents", "h14-py"); code != 0 || !strings.Contains(out, "No route incidents for h14-py") {
		t.Fatalf("incidents: exit %d, out %q err %q", code, out, errOut)
	}
	var report bytes.Buffer
	previous := *main.OsStdout
	*main.OsStdout = &report
	main.RenderRouteMonitorReport(api.RouteMonitorReport{Status: "disabled", Reason: "monitor_disabled"})
	*main.OsStdout = previous
	if out := report.String(); strings.Contains(out, "Deployment:") || !strings.Contains(out, "Coverage:") {
		t.Fatalf("disabled report: %q", out)
	}
	code, out, errOut := run("routes", "requirements", "get", "h14-py")
	if code == 0 || !strings.Contains(out+errOut, "h14-py has none saved yet") {
		t.Fatalf("requirements get: exit %d, out %q err %q", code, out, errOut)
	}
}
