package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r57 (rc.381): `app health` of a worker answered "unsupported"
// and sent the reader to `app health --explain`, which only repeated it.
// The next step for a worker is its pool status.
func TestWorkerHealthPointsAtWorkerStatus(t *testing.T) {
	linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web/health" {
			_ = json.NewEncoder(w).Encode(api.AppHealthResponse{
				Phase: "unsupported", Status: "unknown",
				Checks: []api.AppHealthCheck{{Code: "workload", Status: "unknown", Detail: "HTTP serving health does not assess worker or job execution."}},
			})
			return true
		}
		return false
	})
	_, stdout, stderr := runCLI(t, "app", "health", "web")
	out := stdout + stderr
	if !strings.Contains(out, "worker status web") {
		t.Fatalf("health of a worker does not name worker status:\n%s", out)
	}
	if strings.Contains(out, "app health web --explain") {
		t.Fatalf("health of a worker still sends the reader to --explain:\n%s", out)
	}
}
