package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderDeploymentProgress_EmitsOnlyTransitions(t *testing.T) {
	var out strings.Builder
	d := api.DeploymentResponse{
		Status:           statusLive,
		TrafficPercent:   1,
		CanaryStep:       0,
		CanaryTotalSteps: 4,
		RolloutState:     "rolling_out",
	}
	state := renderDeploymentProgress(&out, d, nil)
	if !strings.Contains(out.String(), "1% traffic · step 1/4 · rolling_out") {
		t.Fatalf("initial progress = %q", out.String())
	}
	initial := out.String()
	state = renderDeploymentProgress(&out, d, state)
	if out.String() != initial {
		t.Fatalf("unchanged progress emitted another line: %q", out.String())
	}

	d.CanaryStep = 1
	d.TrafficPercent = 10
	renderDeploymentProgress(&out, d, state)
	if !strings.Contains(out.String(), "10% traffic · step 2/4 · rolling_out") {
		t.Fatalf("transition progress = %q", out.String())
	}
}

func TestRenderDeploymentProgress_ReportsAbortReason(t *testing.T) {
	var out strings.Builder
	d := api.DeploymentResponse{
		Status:               statusLive,
		TrafficPercent:       10,
		CanaryStep:           1,
		CanaryTotalSteps:     4,
		RolloutState:         rolloutStateAborted,
		RolloutAbortedReason: "error rate exceeded",
	}
	renderDeploymentProgress(&out, d, nil)
	got := out.String()
	if !strings.Contains(got, "10% traffic · step 2/4 · aborted · error rate exceeded") {
		t.Fatalf("abort progress = %q", got)
	}
}
