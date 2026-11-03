package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// PulseWatch F17: omitempty dropped canary_step at step 0, so `release get
// --json` showed a running canary with no step.
func TestDeploymentResponseSendsCanaryStepZero(t *testing.T) {
	body, err := json.Marshal(DeploymentResponse{ID: "d", RolloutState: "rolling_out", CanaryStep: 0, CanaryTotalSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"canary_step":0`) {
		t.Fatalf("deployment JSON = %s, want canary_step 0", body)
	}
}
