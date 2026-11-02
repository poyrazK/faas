// adr: 20261031162858 — a deploy:write credential follows its deployment.

package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A deploy token or a CI OIDC bearer carries only deploy:write: it may read
// its app and its deployment, but not build logs or build status. --wait
// read the deployment once after the log stream was refused and, the build
// still running, exited 3 "stream unreachable" on every CI deploy. It now
// follows the deployment row to the end and does not retry the refused reads.
func TestDeployWaitWithDeployOnlyCredentialFollowsTheDeployment(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.Method+" "+r.URL.Path]++
		reads := calls["GET /v1/deployments/d1"]
		mu.Unlock()
		refused := func() {
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope", "this endpoint requires one of: admin,apps:read"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/ci-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "ci-app", Status: "active"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/ci-app/deployments":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", BuildID: "b1", Status: "pending"})
		case r.URL.Path == "/v1/deployments/d1/logs", r.URL.Path == "/v1/builds/b1":
			refused()
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/d1":
			d := api.DeploymentResponse{ID: "d1", AppID: "a1", BuildID: "b1", Status: "building"}
			if reads >= 3 {
				d.Status, d.TrafficPercent, d.RolloutState = "live", 100, "complete"
			}
			_ = json.NewEncoder(w).Encode(d)
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test: unhandled"))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_deploy_test")
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	started := time.Now()
	code := main.Run([]string{"deploy", "--image", "registry.x/app@sha256:" + strings.Repeat("a", 64), "--name", "ci-app", "--wait", "--yes", "--timeout", "60"})
	mu.Lock()
	defer mu.Unlock()
	if code != 0 {
		t.Fatalf("deploy --wait exit = %d, want 0; calls %v", code, calls)
	}
	if got := calls["GET /v1/deployments/d1"]; got < 3 {
		t.Fatalf("deployment reads = %d, want the wait to follow the row until live; calls %v", got, calls)
	}
	if got := calls["GET /v1/builds/b1"]; got > 1 {
		t.Errorf("build status reads = %d, want a refused read not retried", got)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Errorf("deploy --wait took %s", elapsed)
	}
}
