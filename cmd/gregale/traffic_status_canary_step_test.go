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

// PulseWatch F17: `traffic promote` was refused while a canary ran, and the
// canary's step appeared only in the refusal of a wrong --expected-step guess.
func TestTrafficStatusNamesRollingCanaryStep(t *testing.T) {
	const canaryID = "9f5b1c31-9674-4b9e-9857-6c2003d5c372"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/pulse/deployments" {
			http.Error(w, `{"title":"not found","status":404}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
			{ID: "fe9393d8-0000-4000-8000-000000000002", Revision: 2, Status: "live", TrafficPercent: 99, RolloutState: "complete"},
			{ID: canaryID, Revision: 4, Status: "live", TrafficPercent: 1, RolloutState: "rolling_out", CanaryStep: 0, CanaryTotalSteps: 4},
		}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	stdout, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"release", "traffic", "status", "pulse"})
	restoreErr()
	restoreOut()
	if code != 0 {
		t.Fatalf("traffic status exited %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Canary v4 is rolling out at canary_step=0 (step 1 of 4)", "deployment advance " + canaryID + " --expected-step 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("traffic status output missing %q:\n%s", want, out)
		}
	}
}
