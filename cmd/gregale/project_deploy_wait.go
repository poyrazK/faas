package main

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	projectDeploymentStatusSuperseded = "superseded"
	projectDeploymentStatusCancelled  = "cancelled"
)

// projectDeployPollInitialBackoff is deliberately a variable so the focused
// CLI tests can keep their polling cases fast without changing production
// behavior. Every workload shares the one caller deadline.
var projectDeployPollInitialBackoff = time.Second

type projectWaitResult struct {
	apply       api.ApplyResponse
	timedOut    bool
	interrupted bool
}

// waitForProjectDeployments waits for every build returned by a project apply
// under one wall-clock deadline. Workloads are polled concurrently so a slow
// build cannot consume the entire timeout before a later workload is checked.
// The latest observed deployment/build status is retained even when the wait
// expires, giving both text and JSON callers a useful resumable result.
func waitForProjectDeployments(ctx context.Context, c *Client, apply api.ApplyResponse, timeout time.Duration) projectWaitResult {
	if len(apply.Builds) == 0 {
		return projectWaitResult{apply: apply}
	}
	if c == nil {
		return projectWaitResult{apply: apply, timedOut: true}
	}
	if timeout <= 0 {
		timeout = defaultDeployWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		index    int
		build    api.AppliedBuild
		terminal bool
	}
	results := make(chan result, len(apply.Builds))
	var wg sync.WaitGroup
	for index, build := range apply.Builds {
		if build.Error != "" || (build.DeploymentID == "" && build.BuildID == "") {
			continue
		}
		wg.Add(1)
		go func(index int, build api.AppliedBuild) {
			defer wg.Done()
			updated, terminal := waitForProjectBuild(waitCtx, c, build)
			results <- result{index: index, build: updated, terminal: terminal}
		}(index, build)
	}
	wg.Wait()
	close(results)

	allTerminal := true
	for result := range results {
		apply.Builds[result.index] = result.build
		if !result.terminal {
			allTerminal = false
		}
	}
	return projectWaitResult{
		apply:       apply,
		timedOut:    !allTerminal,
		interrupted: ctx.Err() != nil,
	}
}

func waitForProjectBuild(ctx context.Context, c *Client, build api.AppliedBuild) (api.AppliedBuild, bool) {
	backoff := projectDeployPollInitialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for {
		if build.DeploymentID != "" && !isProjectDeploymentTerminal(build.DeploymentStatus) {
			if deployment, err := c.GetDeployment(ctx, build.DeploymentID); err == nil {
				build.DeploymentStatus = deployment.Status
			}
		}
		if build.BuildID != "" && !isProjectBuildTerminal(build.BuildStatus) {
			if current, err := c.GetBuildsId(ctx, build.BuildID); err == nil {
				build.BuildStatus = current.Status
				build.FailureClass = current.FailureClass
			}
		}

		if projectAppliedBuildTerminal(build) {
			return build, true
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return build, false
		case <-timer.C:
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func isProjectDeploymentTerminal(status string) bool {
	switch status {
	case statusLive, deploymentStatusFailed, projectDeploymentStatusSuperseded, projectDeploymentStatusCancelled:
		return true
	default:
		return false
	}
}

func isProjectBuildTerminal(status string) bool {
	return status == api.BuildStatusSucceeded || status == api.BuildStatusFailed
}

func projectAppliedBuildTerminal(build api.AppliedBuild) bool {
	if build.Error != "" {
		return true
	}
	if build.DeploymentID != "" {
		if !isProjectDeploymentTerminal(build.DeploymentStatus) {
			return false
		}
		// A failed/superseded/cancelled deployment is already the
		// authoritative failure signal. Builders may not produce a
		// terminal build row for those paths, so do not hold the whole
		// project until an unavailable build transition appears.
		if build.DeploymentStatus != statusLive {
			return true
		}
	}
	if build.BuildID != "" && !isProjectBuildTerminal(build.BuildStatus) {
		return false
	}
	return true
}

func projectAppliedBuildFailed(build api.AppliedBuild) bool {
	if build.Error != "" || build.BuildStatus == api.BuildStatusFailed {
		return true
	}
	switch build.DeploymentStatus {
	case deploymentStatusFailed, projectDeploymentStatusSuperseded, projectDeploymentStatusCancelled:
		return true
	default:
		return false
	}
}

func projectApplyHasFailure(apply api.ApplyResponse) bool {
	for _, build := range apply.Builds {
		if projectAppliedBuildFailed(build) {
			return true
		}
	}
	return false
}

func projectApplyExitCode(apply api.ApplyResponse) int {
	infraFailure := false
	for _, build := range apply.Builds {
		if !projectAppliedBuildFailed(build) {
			continue
		}
		if build.FailureClass == "infra" || build.Error == "infra" {
			infraFailure = true
		}
	}
	if infraFailure {
		return 3
	}
	if projectApplyHasFailure(apply) {
		return 1
	}
	return 0
}

// renderProjectApplyBuilds keeps the historical queued receipt for --no-wait
// and renders the observed terminal lifecycle for the default wait path.
func renderProjectApplyBuilds(w io.Writer, builds []api.AppliedBuild, waited bool) {
	for _, build := range builds {
		if build.Error != "" {
			_, _ = fmt.Fprintf(w, "  ! %s: %s\n", build.Slug, build.Error)
			continue
		}
		if !waited {
			marker := ""
			if Enabled() {
				marker = GlyphOK + " "
			}
			_, _ = fmt.Fprintf(w, "  %s%s: deployment=%s build=%s\n", marker, build.Slug, build.DeploymentID, build.BuildID)
			continue
		}
		marker := GlyphOK
		if projectAppliedBuildFailed(build) || !projectAppliedBuildTerminal(build) {
			marker = "!"
		}
		deploymentStatus := build.DeploymentStatus
		if deploymentStatus == "" {
			deploymentStatus = "unknown"
		}
		buildStatus := build.BuildStatus
		if buildStatus == "" {
			buildStatus = "unknown"
		}
		_, _ = fmt.Fprintf(w, "  %s %s: deployment=%s (%s) build=%s (%s)\n",
			marker, build.Slug, build.DeploymentID, deploymentStatus, build.BuildID, buildStatus)
	}
}
