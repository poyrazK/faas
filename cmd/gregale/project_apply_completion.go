package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Project waits read the accepted deployment throughout readiness, rollout and
// configuration activation. They never resubmit source or prepare cron intent.
func pollProjectDeployment(ctx context.Context, client rolloutStatusClient, build api.AppliedBuild) projectApplyWaitResult {
	last := api.DeploymentResponse{ID: build.DeploymentID, BuildID: build.BuildID, Configuration: build.Configuration}
	phase := "deployment"
	remembered := build.Configuration
	backoff := time.Second
	for {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		dep, err := client.GetDeployment(callCtx, build.DeploymentID)
		cancel()
		if err == nil {
			if !sameBindingDeployment(dep.ID, build.DeploymentID) || build.AppID != "" && dep.AppID != build.AppID {
				return projectApplyWaitResult{deployment: last, ok: true, err: "server returned a different deployment or app"}
			}
			if dep.Configuration != nil {
				remembered = dep.Configuration
			}
			last = dep
			done, failure, waiting := projectDeploymentCompletion(dep, remembered)
			if last.Configuration == nil {
				last.Configuration = remembered
			}
			phase = waiting
			if done {
				return projectApplyWaitResult{deployment: last, ok: true, err: failure}
			}
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return projectApplyWaitResult{deployment: last, phase: phase}
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func projectDeploymentCompletion(dep api.DeploymentResponse, accepted *api.DeploymentConfigurationStatus) (bool, string, string) {
	if isTerminalDeploymentStatus(dep.Status) && dep.Status != statusLive {
		return true, "", "deployment"
	}
	if dep.RolloutState == rolloutStateAborted {
		return true, "rollout aborted: " + dep.RolloutAbortedReason, "rollout"
	}
	if failure := projectConfigurationFailure(dep.Configuration); failure != "" {
		return true, failure, "configuration"
	}
	if !isCompletedDeployment(dep) {
		return false, "", "deployment"
	}
	rollout := dep.RolloutState == "rolling_out" || dep.RolloutState == "complete" || dep.CanaryTotalSteps > 0 || dep.RolloutStartedAt != nil || dep.ServiceRolloutHandoff != nil
	if rollout && (!rolloutStatusComplete(dep) || dep.TrafficPercent != 100) {
		return false, "", "rollout"
	}
	config := dep.Configuration
	if config == nil {
		// A managed acceptance receipt must not silently become legacy success
		// when a status response loses the diagnostic field.
		if accepted != nil && accepted.Crons.Status != "not_managed" {
			return false, "", "configuration"
		}
		return true, "", ""
	}
	switch config.Crons.Status {
	case "activated", "not_managed":
		if config.Complete {
			return true, "", ""
		}
	case "pending":
	default:
		return true, "unrecognized deployment configuration status", "configuration"
	}
	return false, "", "configuration"
}

func projectConfigurationFailure(config *api.DeploymentConfigurationStatus) string {
	if config == nil {
		return ""
	}
	cron := config.Crons
	if cron.Status != "conflicted" && cron.Status != "discarded" {
		return ""
	}
	return fmt.Sprintf("cron configuration %s (%s): %s", cron.Status, cron.Reason, cron.Message)
}
