package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// waitForProjectApply closes the gap between project reconciliation and the
// individual deployment lifecycle. ApplyProjectPlan returns as soon as all
// workloads are enqueued; callers that requested the normal wait semantics
// need one terminal answer for every deployment before the command succeeds.
// Workloads are polled concurrently so a ten-app project does not take ten
// sequential readiness windows.
func waitForProjectApply(ctx context.Context, c *Client, apply api.ApplyResponse, deadline time.Duration) (api.ApplyResponse, bool) {
	if c == nil || deadline <= 0 || len(apply.Builds) == 0 {
		return apply, false
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, deadline)
	defer cancelWait()
	result := make([]projectApplyWaitResult, len(apply.Builds))
	var wg sync.WaitGroup
	for i, build := range apply.Builds {
		if build.Error != "" || build.DeploymentID == "" || build.BuildID == "" {
			continue
		}
		wg.Add(1)
		go func(i int, build api.AppliedBuild) {
			defer wg.Done()
			dep := api.DeploymentResponse{ID: build.DeploymentID, BuildID: build.BuildID}
			final, ok := waitForDeploymentReceiptUntil(waitCtx, c, dep, deadline)
			waited := projectApplyWaitResult{deployment: final, ok: ok}
			if ok {
				buildCtx, cancel := context.WithTimeout(waitCtx, 3*time.Second)
				var buildErr error
				waited.build, buildErr = c.GetBuildsId(buildCtx, build.BuildID)
				waited.buildOK = buildErr == nil
				cancel()
			}
			result[i] = waited
		}(i, build)
	}
	wg.Wait()

	timedOut := false
	for i, build := range apply.Builds {
		if build.Error != "" || build.DeploymentID == "" || build.BuildID == "" {
			continue
		}
		waited := result[i]
		switch {
		case !waited.ok:
			apply.Builds[i].DeploymentStatus = "timeout"
			apply.Builds[i].BuildStatus = "timeout"
			apply.Builds[i].Error = "deployment wait timed out"
			timedOut = true
		default:
			apply.Builds[i].DeploymentStatus = waited.deployment.Status
			if waited.buildOK && waited.build.Status != "" {
				apply.Builds[i].BuildStatus = waited.build.Status
			} else {
				apply.Builds[i].BuildStatus = terminalBuildStatusForDeployment(waited.deployment.Status)
			}
			if waited.buildOK && waited.build.Status != api.BuildStatusSucceeded {
				apply.Builds[i].Error = "build " + waited.build.Status
			}
			if waited.deployment.Status == deploymentStatusFailed || waited.deployment.Status == deploymentStatusCancelled || waited.deployment.Status == deploymentStatusSuperseded {
				if waited.deployment.Error != "" {
					apply.Builds[i].Error = "deployment failed: " + waited.deployment.Error
				} else {
					apply.Builds[i].Error = "deployment " + waited.deployment.Status
				}
			}
		}
	}
	return apply, timedOut
}

func terminalBuildStatusForDeployment(status string) string {
	switch status {
	case statusLive:
		return api.BuildStatusSucceeded
	case deploymentStatusCancelled:
		return api.BuildStatusCancelled
	case deploymentStatusFailed:
		return api.BuildStatusFailed
	case deploymentStatusSuperseded:
		return api.BuildStatusCancelled
	default:
		return status
	}
}

type projectApplyWaitResult struct {
	deployment api.DeploymentResponse
	build      api.BuildResponse
	buildOK    bool
	ok         bool
}
