package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// confirmProjectPlan is the approval gate every project deploy passes before
// it creates anything: a blocked plan fails, and an unblocked one needs --yes
// or an interactive confirmation. A non-zero result is the exit code.
func confirmProjectPlan(plan api.PlanResponse, excludeList []string, showAffected, yes bool) int {
	if !plan.CanApply {
		if jsonOutput {
			return jsonOut(writeJSONProblem(planProblem(plan)))
		}
		printPlanText(osStdout, plan, excludeList, showAffected)
		return printErr("Project plan cannot be applied", planBlockedError(plan))
	}
	if yes {
		return 0
	}
	if code := requireAutomationConfirmation(false, "--yes"); code != 0 {
		return code
	}
	// JSON output is intentionally non-interactive: prompting would corrupt
	// the machine-readable stdout stream. Emit the complete plan first, then
	// fail closed so an operator or CI job must make the approval explicit.
	if jsonOutput {
		if code := jsonOut(writeJSON(plan)); code != 0 {
			return code
		}
		return printErr("Confirmation required", errors.New("project deploy requires --yes in JSON mode"))
	}
	if stdoutIsTTY() && stdinIsTTY() {
		if !confirmPlan(osStdout, osStdin, plan, excludeList, showAffected) {
			return printErr("Aborted by user", errors.New("user declined at the confirm prompt"))
		}
		return 0
	}
	// A redirected stream, CI runner, pipeline, or unsupported platform must
	// never turn the absence of a prompt into an implicit approval. Render the
	// same complete plan the TTY prompt would have shown so removals remain
	// visible.
	printPlanText(osStdout, plan, excludeList, true)
	return printErr("Confirmation required", errors.New(
		"project deploy requires --yes when stdin or stdout is not a TTY; review the plan and rerun with --yes"))
}

// validateRepoProjectFlags rejects single-app rollout flags that a connected
// project deploy would otherwise silently ignore.
func validateRepoProjectFlags(explicit map[string]bool, sourceBranch string) error {
	var unsupported []string
	for _, name := range []string{
		"traffic-percent", "no-traffic", "rollout", "canary-preset", "canary-stages", "safe", "rollback-on-5xx",
		"disable-startup-cpu-boost", "mcp-task-migration-override", "reason", "tag", "deployed-by", "pr-number",
		"idempotency-key", "app-protocol", "persist-exclude", "atomic", "verify", "config-file",
	} {
		if explicit[name] {
			unsupported = append(unsupported, "--"+name)
		}
	}
	if sourceBranch != "" {
		unsupported = append(unsupported, "--source-branch")
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("unsupported with --repo --project: %s", strings.Join(unsupported, ", "))
}

// connectedRepoProjectDeploy is `gregale deploy --repo OWNER/NAME --ref REF
// --project`: the control plane fetches the connected repository at the ref,
// the project plan is reviewed, and exactly that reviewed source is applied.
type connectedRepoProjectDeploy struct {
	scan        connectedRepoScanOptions
	yes, wait   bool
	waitTimeout time.Duration
	noTriggers  bool
}

func deployConnectedRepoProject(ctx context.Context, d connectedRepoProjectDeploy) int {
	client, plan, onlyList, excludeList, code := connectedRepoPlan(ctx, d.scan, d.noTriggers)
	if code != 0 {
		return code
	}
	if code := confirmProjectPlan(plan, excludeList, d.scan.showAffected, d.yes); code != 0 {
		return code
	}
	approvalToken := ""
	if plan.EnvironmentProtected {
		approval, err := client.ApproveProjectEnvironment(ctx, d.scan.projectSlug, d.scan.environment,
			api.CreateProjectEnvironmentApprovalRequest{PlanToken: plan.PlanToken})
		if err != nil {
			return printErr("Protected environment approval failed", err)
		}
		approvalToken = approval.ApprovalToken
	}
	session, lock, err := newSavedProjectDeploy(client, d.scan.projectSlug, d.scan.environment,
		api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: plan.SafeRelease}}, "")
	if err != nil {
		return printErr("Could not save project submission attempt; no apply was submitted", err)
	}
	defer func() { _ = lock.Close() }()
	applyCtx := api.ContextWithDeploymentAttempt(ctx, session.record.Project.AttemptID)
	apply, err := client.ApplyCachedProjectPlan(applyCtx, api.ApplyCachedProjectPlanRequest{
		PlanToken: plan.PlanToken, Only: onlyList, Exclude: excludeList, ApprovalToken: approvalToken,
	})
	if err != nil {
		return projectSubmissionFailure(session, err)
	}
	if apply.SubmissionAttemptID == "" {
		session.record.Project.AttemptID = ""
	} else if apply.SubmissionAttemptID != session.record.Project.AttemptID {
		return projectSubmissionFailure(session, errors.New("server returned a different project submission attempt"))
	}
	if saveErr := session.rememberProjectApply(apply); saveErr != nil {
		return renderProjectDeployReceipt(plan, apply, session, false, saveErr)
	}
	timedOut := false
	if d.wait && len(apply.Builds) > 0 {
		apply, timedOut = waitForProjectApply(ctx, client, apply, d.waitTimeout)
	}
	return renderProjectDeployReceipt(plan, apply, session, timedOut, nil)
}
