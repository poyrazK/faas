package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
)

// safeRolloutWaitAllowance is the longest a safe (adaptive) rollout can hold
// once the release is live: every stage before 100% waits for its timer or,
// on a quiet app, for api.CanaryLowTrafficMaxHold before advancing on the
// absence of errors. The default deploy wait covers only the build and the
// snapshot, so a safe deploy of a low-traffic app timed out at 20 minutes and
// exited 3 while the rollout went on to complete (Hunt 12, H11-43 follow-up).
func safeRolloutWaitAllowance() time.Duration {
	preset, ok := canary.LookupPreset(api.DefaultReleaseCanaryPreset)
	if !ok {
		return 0
	}
	var total time.Duration
	for _, stage := range preset.Stages {
		if stage.Percent >= 100 {
			continue
		}
		total += max(stage.Duration, api.CanaryLowTrafficMaxHold)
	}
	return total
}

// safeDeployWaitTimeoutSeconds extends an unset deploy wait by the rollout
// allowance when the deploy waits for a safe rollout to complete.
func safeDeployWaitTimeoutSeconds(waitSeconds int) int {
	return waitSeconds + int(safeRolloutWaitAllowance()/time.Second)
}
