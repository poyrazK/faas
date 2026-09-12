package main

import (
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectApplyStatus is the CLI's interpretation of the per-workload build
// results returned by POST /v1/projects. The server deliberately keeps
// reconciliation and build enqueue independent, so a 200 response can still
// contain failed (or missing) build results.
type projectApplyStatus struct {
	appsReconciled int
	buildsQueued   int
	buildsFailed   int
	// missingBuilds is the number of expected build rows that the server did
	// not return. It is kept separately so the text renderer can explain an
	// empty/short builds array without inventing a workload slug.
	missingBuilds int
}

// summarizeProjectApply classifies an apply response for both human and JSON
// output. A successful build row must carry both IDs; an error row or an
// incomplete row is a failed build. ApplyResponse.Apps is the authoritative
// added/changed set, so it tells us how many rows the enqueue loop should have
// returned without mistaking a matched-but-unchanged update for a build.
func summarizeProjectApply(apply api.ApplyResponse) projectApplyStatus {
	status := projectApplyStatus{
		appsReconciled: len(apply.Apps),
	}

	for _, build := range apply.Builds {
		if build.Error != "" || build.DeploymentID == "" || build.BuildID == "" {
			status.buildsFailed++
			continue
		}
		status.buildsQueued++
	}

	expected := len(apply.Apps)
	if missing := expected - len(apply.Builds); missing > 0 {
		status.missingBuilds = missing
		status.buildsFailed += missing
	}
	return status
}

// renderProjectApplyResult prints the complete apply receipt and returns the
// process status. Every build row is retained, including failures, while the
// summary makes partial success explicit to operators and shell callers.
func renderProjectApplyResult(w io.Writer, plan api.PlanResponse, apply api.ApplyResponse) int {
	status := summarizeProjectApply(apply)
	if status.buildsFailed > 0 {
		PrintFail(w, "Project %s applied with failures", apply.ProjectID)
	} else {
		PrintOK(w, "Created project %s with %d app(s) and %d cron(s)",
			apply.ProjectID, len(apply.Apps), len(plan.Crons))
	}

	// Preserve the existing post-apply rescue signal on the human path.
	renderApplyRescue(w, apply)

	for _, build := range apply.Builds {
		switch {
		case build.Error != "":
			_, _ = fmt.Fprintf(w, "  ! %s: %s\n", build.Slug, build.Error)
		case build.DeploymentID == "" || build.BuildID == "":
			_, _ = fmt.Fprintf(w, "  ! %s: incomplete build result (deployment_id/build_id missing)\n", build.Slug)
		default:
			_, _ = fmt.Fprintf(w, "  ✓ %s: deployment=%s build=%s\n", build.Slug, build.DeploymentID, build.BuildID)
		}
	}
	if status.missingBuilds > 0 {
		_, _ = fmt.Fprintf(w, "  ! missing build results: %d workload(s) were expected but not returned\n", status.missingBuilds)
	}

	_, _ = fmt.Fprintf(w, "Summary: apps reconciled=%d, builds queued=%d, builds failed=%d\n",
		status.appsReconciled, status.buildsQueued, status.buildsFailed)
	if status.buildsFailed > 0 {
		return 1
	}
	return 0
}

// reportProjectApplyFailure keeps --json stdout machine-readable while still
// explaining the non-zero exit to an operator watching stderr.
func reportProjectApplyFailure(w io.Writer, status projectApplyStatus) {
	_, _ = fmt.Fprintf(w, "project apply failed: apps reconciled=%d, builds queued=%d, builds failed=%d\n",
		status.appsReconciled, status.buildsQueued, status.buildsFailed)
}
