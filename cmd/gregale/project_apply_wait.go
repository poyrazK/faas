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
	waited, timeouts := waitForProjectApplyTimeouts(ctx, c, apply, deadline)
	return waited, len(timeouts) > 0
}

// projectWaitTimeout names a workload the wait stopped watching before its
// deployment completed. It is not a failure: the server keeps processing,
// so the row keeps no error and the last status the CLI saw, and the
// receipt lists it under wait_timed_out. Reporting these rows as
// status "timeout" with an error printed "project apply failed … builds
// failed=N" for a deploy that was still running (CLI audit 2026-10-31).
type projectWaitTimeout struct {
	Slug         string `json:"slug"`
	DeploymentID string `json:"deployment_id"`
	// Phase is the part of completion the wait was on: deployment,
	// configuration or rollout.
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

func projectWaitTimeoutFor(timeouts []projectWaitTimeout, deploymentID string) (projectWaitTimeout, bool) {
	for _, timeout := range timeouts {
		if deploymentID != "" && timeout.DeploymentID == deploymentID {
			return timeout, true
		}
	}
	return projectWaitTimeout{}, false
}

// waitForProjectApplyTimeouts is waitForProjectApply that also names the
// workloads whose wait timed out.
func waitForProjectApplyTimeouts(ctx context.Context, c *Client, apply api.ApplyResponse, deadline time.Duration) (api.ApplyResponse, []projectWaitTimeout) {
	if c == nil || deadline <= 0 || len(apply.Builds) == 0 {
		return apply, nil
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, deadline)
	defer cancelWait()
	result := make([]projectApplyWaitResult, len(apply.Builds))
	var wg sync.WaitGroup
	for i, build := range apply.Builds {
		if build.Error != "" || build.DeploymentID == "" {
			continue
		}
		wg.Add(1)
		go func(i int, build api.AppliedBuild) {
			defer wg.Done()
			waited := pollProjectDeployment(waitCtx, c, build)
			if waited.ok && build.BuildID != "" {
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

	var timeouts []projectWaitTimeout
	for i, build := range apply.Builds {
		if build.Error != "" || build.DeploymentID == "" {
			continue
		}
		waited := result[i]
		apply.Builds[i].Configuration = waited.deployment.Configuration
		if apply.Builds[i].Configuration == nil {
			apply.Builds[i].Configuration = build.Configuration
		}
		switch {
		case !waited.ok:
			phase := waited.phase
			if phase == "" {
				phase = "deployment"
			}
			message := phase + " wait timed out; server continues processing"
			if ctx.Err() == context.Canceled {
				message = phase + " wait interrupted; server continues processing"
			}
			// The last status the server reported, not an invented one;
			// empty when no read succeeded.
			apply.Builds[i].DeploymentStatus = waited.deployment.Status
			if phase != "deployment" && build.BuildID != "" {
				apply.Builds[i].BuildStatus = terminalBuildStatusForDeployment(waited.deployment.Status)
			}
			timeouts = append(timeouts, projectWaitTimeout{Slug: build.Slug, DeploymentID: build.DeploymentID, Phase: phase, Message: message})
		default:
			apply.Builds[i].DeploymentStatus = waited.deployment.Status
			if build.BuildID != "" {
				if waited.buildOK && waited.build.Status != "" {
					apply.Builds[i].BuildStatus = waited.build.Status
				} else {
					apply.Builds[i].BuildStatus = terminalBuildStatusForDeployment(waited.deployment.Status)
				}
			}
			apply.Builds[i].Error = waited.err
			if waited.buildOK && waited.build.Status != api.BuildStatusSucceeded {
				apply.Builds[i].Error = "build " + waited.build.Status
			}
			if isTerminalDeploymentStatus(waited.deployment.Status) && waited.deployment.Status != statusLive {
				if waited.deployment.Error != "" {
					apply.Builds[i].Error = "deployment failed: " + waited.deployment.Error
				} else {
					apply.Builds[i].Error = "deployment " + waited.deployment.Status
				}
			}
		}
	}
	return apply, timeouts
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
	err        string
	phase      string
}
