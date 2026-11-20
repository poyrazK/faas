package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-20 (R10-8): a canary whose rollout was aborted keeps status
// "live" at 0% traffic, and release list showed it as plain "live" beside
// the release that serves again.
func TestReleaseListShowsAbortedCanary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/web/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "d2", AppID: "a1", Status: "live", Kind: "app", RolloutState: "aborted", CreatedAt: "2026-11-20T02:00:00Z"},
				{ID: "d1", AppID: "a1", Status: "live", Kind: "app", TrafficPercent: 100, RolloutState: "completed", CreatedAt: "2026-11-20T01:00:00Z"},
			}})
		case "/v1/apps/web":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "web", Status: "running"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	stdout, _, restore := main.SwapIO(t)
	code := main.Run([]string{"deployments", "--app", "web"})
	restore()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stdout.String())
	}
	var aborted, live string
	for _, line := range strings.Split(stdout.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "d2"):
			aborted = line
		case strings.HasPrefix(line, "d1"):
			live = line
		}
	}
	if !strings.Contains(aborted, "aborted") || strings.Contains(aborted, " live ") {
		t.Fatalf("aborted canary row = %q, want status aborted", aborted)
	}
	if !strings.Contains(live, "live") {
		t.Fatalf("serving release row = %q, want live", live)
	}
}
