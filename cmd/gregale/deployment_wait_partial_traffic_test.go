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

// boa multi-service deploy (2026-11-21): `release wait` without --rollout
// reported boa-contacts v3 "live" while its canary served 1% and the broken
// v1 served 99%. The receipt names the share and how to wait for 100%.
func TestDeploymentWaitNamesPartialTraffic(t *testing.T) {
	const id = "6c1c7d0e-8f0e-4a1e-9a55-0b8f1c4d2e11"
	for _, tc := range []struct {
		name       string
		dep        api.DeploymentResponse
		wantStdout string
		wantStderr string
	}{
		{name: "canary at 1%", dep: api.DeploymentResponse{ID: id, Status: "live", TrafficPercent: 1, RolloutState: "rolling_out", CanaryStep: 1, CanaryTotalSteps: 4},
			wantStdout: "serves 1% of traffic (canary step 1 of 4)", wantStderr: "--rollout"},
		{name: "manual split", dep: api.DeploymentResponse{ID: id, Status: "live", TrafficPercent: 30},
			wantStdout: "serves 30% of traffic", wantStderr: "release traffic status"},
		{name: "all traffic", dep: api.DeploymentResponse{ID: id, Status: "live", TrafficPercent: 100},
			wantStdout: "Deployment " + id + " is live."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/deployments/"+id {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.dep)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			stdout, readStderr, restore := main.SwapIO(t)
			code := main.Run([]string{"release", "wait", id, "--timeout", "30"})
			stderr := readStderr()
			restore()
			if code != 0 {
				t.Fatalf("exit = %d; stdout %q stderr %q", code, stdout, stderr)
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Fatalf("stdout %q, want %q", stdout, tc.wantStdout)
			}
			if tc.wantStderr == "" && strings.TrimSpace(stderr) != "" || !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr %q, want %q", stderr, tc.wantStderr)
			}
		})
	}
}
