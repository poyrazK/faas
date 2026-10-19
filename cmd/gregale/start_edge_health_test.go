package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartPublicEdgeHealthDoesNotVerifyAppResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			stdout, _ := main.StartTestEnvironment(t)
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
			calls := 0
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("X-Faas-Health-Source", "edge")
				w.Header().Set(api.DeploymentIDHeader, "d1")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"status":"ok","source":"edge"}`))
			}))
			t.Cleanup(app.Close)
			runner := main.StartTestRunner(t, main.StartTestSource(t), "")
			(*runner.Session()).DeploymentID = "d1"
			code := runner.PublicRequest(api.AppResponse{URL: app.URL}, "/healthz")
			if code != 1 || calls != 1 || (*runner.LastRequest()) != nil || (*runner.RequestFailure()) == nil {
				t.Fatalf("edge health verified as app response: exit=%d calls=%d", code, calls)
			}
			if !strings.Contains(stdout.String(), "The gateway answered this health check") || strings.Contains(stdout.String(), "Your app answered") {
				t.Fatalf("missing app-route guidance: %s", stdout)
			}
		})
	}
}
