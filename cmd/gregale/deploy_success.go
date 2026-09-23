package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// deploymentReceiptFetchTimeout keeps a successful deploy from hanging on a
// best-effort read of the durable hosting evidence. The deployment itself is
// already live at this point; receipt rendering must never turn that success
// into a slow or failed command.
const (
	deploymentReceiptFetchTimeout = 3 * time.Second
	rolloutPollInterval           = 2 * time.Second
	rolloutStateComplete          = "complete"
	rolloutStateAborted           = "aborted"
	// defaultDeployWaitTimeout covers the server's complete build budget
	// (currently 15 minutes) plus five minutes for security scanning,
	// snapshot preparation, readiness, and the post-readiness smoke. The
	// explicit --timeout flag remains available for unusually slow builds.
	defaultDeployWaitTimeout        = time.Duration(api.BuildE2ETimeoutSeconds+5*60) * time.Second
	defaultDeployWaitTimeoutSeconds = int(defaultDeployWaitTimeout / time.Second)
)

// deploymentWithReceipt fetches the durable deployment row after readiness.
// The create response and the SSE status frame intentionally carry only the
// lifecycle state, while hosting_receipt is persisted by the readiness path.
func deploymentWithReceipt(ctx context.Context, c *Client, dep api.DeploymentResponse) api.DeploymentResponse {
	if c == nil || dep.ID == "" {
		return dep
	}
	readCtx, cancel := context.WithTimeout(ctx, deploymentReceiptFetchTimeout)
	defer cancel()
	got, err := c.GetDeployment(readCtx, dep.ID)
	if err != nil || got.ID == "" {
		return dep
	}
	return got
}

// deploymentWithReleaseSummary fetches the durable predecessor/diff data
// after a deployment is live. Like the hosting receipt read above, this is a
// best-effort enrichment: a summary endpoint failure must never change a
// successful deploy into a failed command.
func deploymentWithReleaseSummary(ctx context.Context, c *Client, appSlug, deploymentID string) (api.DeploymentSummaryResponse, bool) {
	if c == nil || appSlug == "" || deploymentID == "" {
		return api.DeploymentSummaryResponse{}, false
	}
	readCtx, cancel := context.WithTimeout(ctx, deploymentReceiptFetchTimeout)
	defer cancel()
	summary, err := c.GetAppDeploymentSummary(readCtx, appSlug, deploymentID)
	if err != nil {
		return api.DeploymentSummaryResponse{}, false
	}
	return summary, true
}

// deploymentAppURL resolves the customer-facing URL for terminal deploy
// output. The lookup is deliberately best-effort so a metadata read cannot
// turn an already successful deployment into a failed command.
func deploymentAppURL(ctx context.Context, c *Client, appSlug string) string {
	if app, ok := deploymentApp(ctx, c, appSlug); ok {
		return canonicalAppURL(app)
	}
	return deployedAppURL(appSlug)
}

// deploymentApp is the best-effort app read behind the success output.
func deploymentApp(ctx context.Context, c *Client, appSlug string) (api.AppResponse, bool) {
	if c == nil || appSlug == "" {
		return api.AppResponse{}, false
	}
	readCtx, cancel := context.WithTimeout(ctx, deploymentReceiptFetchTimeout)
	defer cancel()
	app, err := c.GetApp(readCtx, appSlug)
	return app, err == nil
}

// renderDeploymentAccess says when the new URL rejects anonymous requests.
// Hobby and above default to require_authn (ADR-080), and the post-deploy
// verifier authenticates, so "verified, 200" followed by a 401 from curl
// looked like a broken deploy (issue #3362).
func renderDeploymentAccess(w io.Writer, app api.AppResponse, appSlug string) {
	if !app.RequireAuthn {
		return
	}
	if app.PublicAuth.Mode == api.AppPublicAuthModeBasic {
		PrintProgress(w, "Access: requests need HTTP Basic credentials (%s).", formatAppAuth(app))
	} else {
		PrintProgress(w, "Access: requests need Authorization: Bearer <api-key> (%s).", formatAppAuth(app))
	}
	PrintProgress(w, "  make the URL public: gregale app %s --no-require-authn", appSlug)
}

// deploymentPreviewURL resolves the immutable per-deployment URL after a
// dark deployment is live. This is receipt enrichment, not a deployment
// prerequisite: platforms without the preview zone still get a useful
// promotion command and can inspect the revision later.
func deploymentPreviewURL(ctx context.Context, c *Client, deploymentID string) string {
	if c == nil || deploymentID == "" {
		return ""
	}
	readCtx, cancel := context.WithTimeout(ctx, deploymentReceiptFetchTimeout)
	defer cancel()
	preview, err := c.GetDeploymentURL(readCtx, deploymentID)
	if err != nil || !preview.Alive {
		return ""
	}
	return preview.URL
}

func deploymentCommandRef(dep api.DeploymentResponse) string {
	if label := renderRevision(dep.Revision); label != "" {
		return label
	}
	return dep.ID
}

// deploymentPromotionCommand includes a production-revision precondition when
// a single live sibling owns 100% of traffic. A split or failed read cannot
// safely provide a copy-paste promotion command.
func deploymentPromotionCommand(ctx context.Context, c *Client, appSlug string, dep api.DeploymentResponse) (command, servingRef string) {
	if c == nil || appSlug == "" {
		return "", ""
	}
	readCtx, cancel := context.WithTimeout(ctx, deploymentReceiptFetchTimeout)
	defer cancel()
	deployments, err := c.ListAppDeploymentsAll(readCtx, appSlug)
	if err != nil {
		return "", ""
	}
	command = fmt.Sprintf("gregale traffic promote --app %s --deployment %s", appSlug, deploymentCommandRef(dep))
	for _, sibling := range deployments {
		if sibling.ID == dep.ID || sibling.Status != statusLive || sibling.TrafficPercent == 0 {
			continue
		}
		if sibling.TrafficPercent != 100 || servingRef != "" {
			return "", ""
		}
		servingRef = deploymentCommandRef(sibling)
	}
	if servingRef != "" {
		command += " --if-serving " + servingRef
	}
	return command, servingRef
}

func renderQueuedDeployment(dep api.DeploymentResponse, appURL string, darkDeploy bool) {
	if !darkDeploy {
		PrintOK(osStdout, "Deployment %s queued. %s", dep.ID, appURL)
		return
	}
	PrintOK(osStdout, "Deployment %s queued with 0%% production traffic.", dep.ID)
	PrintProgress(osStdout, "Production traffic remains unchanged. %s", appURL)
}

// renderSuccessfulDeployment prints the existing success/cold-wake copy and
// appends the verified zero-config profile and smoke evidence when the API has
// persisted a hosting receipt.
func renderSuccessfulDeployment(ctx context.Context, c *Client, dep api.DeploymentResponse, appSlug string) int {
	return renderSuccessfulDeploymentWithOptions(ctx, c, dep, appSlug, false)
}

func renderSuccessfulDeploymentWithOptions(ctx context.Context, c *Client, dep api.DeploymentResponse, appSlug string, darkDeploy bool) int {
	final := deploymentWithReceipt(ctx, c, dep)
	app, appOK := deploymentApp(ctx, c, appSlug)
	appURL := deployedAppURL(appSlug)
	if appOK {
		appURL = canonicalAppURL(app)
	}
	if final.CanaryTotalSteps > 0 && final.RolloutState == rolloutStateAborted {
		reason := final.RolloutAbortedReason
		if reason == "" {
			reason = "the rollout was aborted"
		}
		PrintFail(osStderr, "Safe rollout stopped before reaching 100%% traffic for %s: %s", appSlug, reason)
		PrintProgress(osStderr, "inspect: gregale deployment %s", final.ID)
		return 1
	}
	if final.CanaryTotalSteps > 0 && !deploymentRolloutComplete(final) {
		step := final.CanaryStep + 1
		if step > final.CanaryTotalSteps {
			step = final.CanaryTotalSteps
		}
		// ADR-198: name the revision that just went live. During a canary the
		// customer is watching two revisions at once, so "candidate" without
		// saying WHICH one is the least useful moment to omit it.
		if label := renderRevision(final.Revision); label != "" {
			PrintOK(osStdout, "Candidate %s live. %s", label, appURL)
		} else {
			PrintOK(osStdout, "Candidate live. %s", appURL)
		}
		PrintProgress(osStdout, "Rollout: %d%% traffic · step %d/%d · in progress", final.TrafficPercent, step, final.CanaryTotalSteps)
		PrintProgress(osStdout, "follow: gregale deployment wait %s --rollout", final.ID)
	} else if darkDeploy {
		if final.TrafficPercent != 0 {
			PrintFail(osStderr, "Deployment %s became live with %d%% production traffic; expected 0%%. Inspect with: gregale traffic status %s", final.ID, final.TrafficPercent, appSlug)
			return 1
		}
		ref := deploymentCommandRef(final)
		PrintOK(osStdout, "Staged %s with 0%% production traffic.", ref)
		if previewURL := deploymentPreviewURL(ctx, c, final.ID); previewURL != "" {
			PrintProgress(osStdout, "Preview: %s", previewURL)
		} else {
			PrintProgress(osStdout, "Preview: gregale deploys show %s --app %s --url", ref, appSlug)
		}
		promotionCommand, servingRef := deploymentPromotionCommand(ctx, c, appSlug, final)
		if servingRef != "" {
			PrintProgress(osStdout, "Production remains on %s. %s", servingRef, appURL)
		} else {
			PrintProgress(osStdout, "Production traffic remains unchanged. %s", appURL)
		}
		if promotionCommand != "" {
			PrintProgress(osStdout, "Promote: %s", promotionCommand)
		} else {
			PrintProgress(osStdout, "Promotion: inspect current traffic with gregale traffic status %s", appSlug)
		}
	} else if label := renderRevision(final.Revision); label != "" {
		PrintOK(osStdout, "Deployed %s. %s", label, appURL)
	} else {
		PrintOK(osStdout, "Deployed. %s", appURL)
	}
	if !darkDeploy {
		printDeployColdWakeSentence()
	}
	if appOK {
		renderDeploymentAccess(osStdout, app, appSlug)
	}
	if cache := formatBuildCacheSummary(final.BuildCacheStatus, final.CacheKeySHA256); cache != "" {
		PrintProgress(osStdout, "Build cache: %s", cache)
	}
	renderDeploymentHostingReceipt(osStdout, final.APIHostingReceipt)
	if !darkDeploy {
		if summary, ok := deploymentWithReleaseSummary(ctx, c, appSlug, final.ID); ok {
			renderDeploymentReleaseSummary(osStdout, summary, appSlug)
		}
	}
	return 0
}

// deploymentRolloutComplete is deliberately separate from readiness
// completion. A canary deployment can be live and routable while it is still
// below 100% traffic; safe deploys must wait for the rollout state machine to
// finish before reporting success.
func deploymentRolloutComplete(dep api.DeploymentResponse) bool {
	if dep.Status != statusLive {
		return false
	}
	if dep.CanaryTotalSteps <= 0 {
		return true
	}
	return dep.RolloutState == rolloutStateComplete
}

func deploymentRolloutTerminal(dep api.DeploymentResponse) bool {
	if dep.Status != statusLive {
		return isTerminalDeploymentStatus(dep.Status)
	}
	return dep.CanaryTotalSteps <= 0 || dep.RolloutState == rolloutStateComplete || dep.RolloutState == rolloutStateAborted
}

// waitForDeploymentRollout polls the durable deployment row after readiness
// has completed. It returns on full rollout, an aborted/terminal deployment,
// or context cancellation.
func waitForDeploymentRollout(ctx context.Context, c *Client, dep api.DeploymentResponse) (api.DeploymentResponse, bool) {
	if deploymentRolloutTerminal(dep) {
		return deploymentWithReceipt(ctx, c, dep), true
	}
	if c == nil {
		return dep, false
	}
	last := dep
	for {
		got, err := c.GetDeployment(ctx, dep.ID)
		if err == nil {
			last = got
			if deploymentRolloutTerminal(got) {
				return deploymentWithReceipt(ctx, c, got), true
			}
		}
		timer := time.NewTimer(rolloutPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return last, false
		case <-timer.C:
		}
	}
}

func renderDeploymentReleaseSummary(w io.Writer, summary api.DeploymentSummaryResponse, appSlug string) {
	_, _ = fmt.Fprintln(w, "Release summary:")
	switch {
	case summary.Previous == nil:
		_, _ = fmt.Fprintln(w, "  Changes: initial release")
	case len(summary.Changes) == 0:
		_, _ = fmt.Fprintf(w, "  Changes since %s: none\n", deploymentLabel(*summary.Previous))
	default:
		_, _ = fmt.Fprintf(w, "  Changes since %s:\n", deploymentLabel(*summary.Previous))
		for _, change := range summary.Changes {
			_, _ = fmt.Fprintf(w, "    %-18s %s -> %s\n", change.Field,
				formatSummaryValue(change.Before), formatSummaryValue(change.After))
		}
	}
	if summary.RollbackTargetID == "" {
		_, _ = fmt.Fprintln(w, "  Rollback: unavailable (no previous release)")
		return
	}
	// ADR-198: print the revision handle when the target has one. This line is
	// meant to be copy-pasted, and a uuid is the one thing in this output a
	// human cannot retype or recognise later. Rows predating the revision
	// column still fall back to the id so the command always works.
	target := summary.RollbackTargetID
	if label := renderRevision(summary.RollbackTargetRevision); label != "" {
		target = label
	}
	_, _ = fmt.Fprintf(w, "  Rollback: gregale rollback %s --to %s\n", appSlug, target)
}

// waitForDeploymentReceiptUntil is the timeout-aware implementation used by
// JSON deploys whenever lifecycle waiting is enabled (the default).
func waitForDeploymentReceiptUntil(ctx context.Context, c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.DeploymentResponse, bool) {
	if isCompletedDeployment(dep) {
		return deploymentWithReceipt(ctx, c, dep), true
	}
	if c == nil {
		return api.DeploymentResponse{}, false
	}
	return pollDeploymentFinalUntilContext(ctx, c, dep, deadline)
}

// deploymentWaitResumeCommand is deliberately emitted as a complete command
// so a timed-out deploy can be resumed without reconstructing flags from logs.
func deploymentWaitResumeCommand(deploymentID string, deadline time.Duration) string {
	return deploymentWaitResumeCommandWithRollout(deploymentID, deadline, false)
}

func deploymentWaitResumeCommandWithRollout(deploymentID string, deadline time.Duration, rollout bool) string {
	seconds := int(deadline / time.Second)
	if seconds <= 0 {
		seconds = defaultDeployWaitTimeoutSeconds
	}
	rolloutFlag := ""
	if rollout {
		rolloutFlag = " --rollout"
	}
	return fmt.Sprintf("gregale deployment wait %s%s --timeout %d", deploymentID, rolloutFlag, seconds)
}

func warnDeploymentWaitTimeout(appSlug, deploymentID string, deadline time.Duration) {
	PrintWarn(osStderr,
		"deployment wait timed out after %s; server continues processing; resume with: %s; follow logs with: gregale logs %s --deployment %s --follow",
		deadline, deploymentWaitResumeCommand(deploymentID, deadline), appSlug, deploymentID)
}

func warnDeploymentRolloutTimeout(appSlug, deploymentID string, deadline time.Duration) {
	PrintWarn(osStderr,
		"safe rollout timed out after %s; server continues processing; resume with: %s; follow logs with: gregale logs %s --deployment %s --follow",
		deadline, deploymentWaitResumeCommandWithRollout(deploymentID, deadline, true), appSlug, deploymentID)
}

func warnDeploymentTimeoutForMode(appSlug, deploymentID string, deadline time.Duration, waitForRollout bool) {
	if waitForRollout {
		warnDeploymentRolloutTimeout(appSlug, deploymentID, deadline)
		return
	}
	warnDeploymentWaitTimeout(appSlug, deploymentID, deadline)
}

// writeWaitedDeploymentReceiptUntil emits a single terminal-or-timeout JSON
// object using the caller's wait deadline. A timeout still returns the
// accepted deployment id so automation can resume with `deployment wait`.
func writeWaitedDeploymentReceiptUntil(ctx context.Context, c *Client, dep api.DeploymentResponse, prov *zeroConfigProvenance, appURL, sourceSHA256, appSlug string, deadline time.Duration) int {
	return writeWaitedDeploymentReceiptUntilWithOptions(ctx, c, dep, prov, appURL, sourceSHA256, appSlug, deadline, false, false)
}

func writeWaitedDeploymentReceiptUntilWithOptions(ctx context.Context, c *Client, dep api.DeploymentResponse, prov *zeroConfigProvenance, appURL, sourceSHA256, appSlug string, deadline time.Duration, waitForRollout, darkDeploy bool, simplePlans ...*simpleapp.Plan) int {
	if deadline <= 0 {
		deadline = defaultDeployWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	final, ok := waitForDeploymentReceiptUntil(waitCtx, c, dep, deadline)
	if ok && waitForRollout && final.Status == statusLive && !deploymentRolloutComplete(final) {
		final, ok = waitForDeploymentRollout(waitCtx, c, final)
	}
	if !ok {
		resumeCommand := deploymentWaitResumeCommandWithRollout(dep.ID, deadline, waitForRollout)
		if waitForRollout {
			PrintWarn(osStderr, "safe deployment did not reach 100%% traffic before the wait deadline; server continues processing; resume with: %s", resumeCommand)
		} else {
			PrintWarn(osStderr, "deployment did not reach a terminal state before the wait deadline; server continues processing; resume with: %s", resumeCommand)
		}
		receiptDep := dep
		if final.ID != "" {
			receiptDep = final
		}
		receipt := newDeployReceipt(receiptDep, prov, appURL, sourceSHA256, simplePlans...)
		receipt.TimedOut = true
		receipt.ResumeCommand = resumeCommand
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
		return 3
	}
	receipt := newDeployReceipt(final, prov, appURL, sourceSHA256, simplePlans...)
	if final.Status == statusLive && darkDeploy && final.TrafficPercent == 0 {
		receipt.PreviewURL = deploymentPreviewURL(ctx, c, final.ID)
		receipt.PromotionCommand, _ = deploymentPromotionCommand(ctx, c, appSlug, final)
	}
	if final.Status == statusLive && !darkDeploy {
		if summary, summaryOK := deploymentWithReleaseSummary(ctx, c, appSlug, final.ID); summaryOK {
			receipt.ReleaseSummary = newDeployReleaseSummary(summary, appSlug)
		}
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if waitForRollout && final.CanaryTotalSteps > 0 && final.RolloutState == rolloutStateAborted {
		return 1
	}
	if final.Status != statusLive {
		return 1
	}
	if darkDeploy && final.TrafficPercent != 0 {
		PrintFail(osStderr, "Deployment %s became live with %d%% production traffic; expected 0%%", final.ID, final.TrafficPercent)
		return 1
	}
	return 0
}
