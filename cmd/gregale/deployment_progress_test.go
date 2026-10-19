package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderDeploymentProgress_EmitsOnlyTransitions(t *testing.T) {
	var out strings.Builder
	d := api.DeploymentResponse{
		Status:           main.StatusLive,
		TrafficPercent:   1,
		CanaryStep:       0,
		CanaryTotalSteps: 4,
		RolloutState:     "rolling_out",
	}
	state := main.RenderDeploymentProgress(&out, d, nil)
	if !strings.Contains(out.String(), "1% traffic · step 1/4 · rolling_out") {
		t.Fatalf("initial progress = %q", out.String())
	}
	initial := out.String()
	state = main.RenderDeploymentProgress(&out, d, state)
	if out.String() != initial {
		t.Fatalf("unchanged progress emitted another line: %q", out.String())
	}

	d.CanaryStep = 1
	d.TrafficPercent = 10
	main.RenderDeploymentProgress(&out, d, state)
	if !strings.Contains(out.String(), "10% traffic · step 2/4 · rolling_out") {
		t.Fatalf("transition progress = %q", out.String())
	}
}

func TestRenderDeploymentProgress_ReportsAbortReason(t *testing.T) {
	var out strings.Builder
	d := api.DeploymentResponse{
		Status:               main.StatusLive,
		TrafficPercent:       10,
		CanaryStep:           1,
		CanaryTotalSteps:     4,
		RolloutState:         main.RolloutStateAborted,
		RolloutAbortedReason: "error rate exceeded",
	}
	main.RenderDeploymentProgress(&out, d, nil)
	got := out.String()
	if !strings.Contains(got, "10% traffic · step 2/4 · aborted · error rate exceeded") {
		t.Fatalf("abort progress = %q", got)
	}
}
