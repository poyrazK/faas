package main

import (
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

type deploymentProgressSnapshot struct {
	status       string
	rolloutState string
	step         int
	total        int
	traffic      int
	reason       string
}

// renderDeploymentProgress emits one human-readable line when a deployment's
// lifecycle or rollout position changes. Keeping the snapshot local to the
// wait command avoids a noisy line every polling interval while still making
// each canary transition visible to an operator.
func renderDeploymentProgress(w io.Writer, d api.DeploymentResponse, previous *deploymentProgressSnapshot) *deploymentProgressSnapshot {
	current := deploymentProgressSnapshot{
		status:       d.Status,
		rolloutState: d.RolloutState,
		step:         d.CanaryStep,
		total:        d.CanaryTotalSteps,
		traffic:      d.TrafficPercent,
		reason:       d.RolloutAbortedReason,
	}
	if previous != nil && *previous == current {
		return previous
	}
	if d.CanaryTotalSteps <= 0 {
		_, _ = fmt.Fprintf(w, "Deployment: %s\n", nonEmptyProgressState(d.Status, "unknown"))
		return &current
	}
	step := d.CanaryStep + 1
	if d.RolloutState == rolloutStateComplete || step > d.CanaryTotalSteps {
		step = d.CanaryTotalSteps
	}
	state := nonEmptyProgressState(d.RolloutState, d.Status)
	if d.RolloutState == rolloutStateAborted && d.RolloutAbortedReason != "" {
		_, _ = fmt.Fprintf(w, "Rollout: %d%% traffic · step %d/%d · %s · %s\n", d.TrafficPercent, step, d.CanaryTotalSteps, state, d.RolloutAbortedReason)
		return &current
	}
	_, _ = fmt.Fprintf(w, "Rollout: %d%% traffic · step %d/%d · %s\n", d.TrafficPercent, step, d.CanaryTotalSteps, state)
	return &current
}

func nonEmptyProgressState(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
