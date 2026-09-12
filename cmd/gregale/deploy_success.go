package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// deploymentReceiptFetchTimeout keeps a successful deploy from hanging on a
// best-effort read of the durable hosting evidence. The deployment itself is
// already live at this point; receipt rendering must never turn that success
// into a slow or failed command.
const (
	deploymentReceiptFetchTimeout = 3 * time.Second
	// defaultDeployWaitTimeout preserves the historical five-minute
	// deploy wait while allowing `gregale deploy --timeout` to override
	// the bounded wait for slower builds or CI jobs.
	defaultDeployWaitTimeout = 5 * time.Minute
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

// renderSuccessfulDeployment prints the existing success/cold-wake copy and
// appends the verified zero-config profile and smoke evidence when the API has
// persisted a hosting receipt.
func renderSuccessfulDeployment(ctx context.Context, c *Client, dep api.DeploymentResponse, appSlug string) int {
	final := deploymentWithReceipt(ctx, c, dep)
	PrintOK(osStdout, "Deployed. %s", deployedAppURL(appSlug))
	printDeployColdWakeSentence()
	if cache := formatBuildCacheSummary(final.BuildCacheStatus, final.CacheKeySHA256); cache != "" {
		PrintProgress(osStdout, "Build cache: %s", cache)
	}
	renderDeploymentHostingReceipt(osStdout, final.APIHostingReceipt)
	if summary, ok := deploymentWithReleaseSummary(ctx, c, appSlug, final.ID); ok {
		renderDeploymentReleaseSummary(osStdout, summary, appSlug)
	}
	return 0
}

func renderDeploymentReleaseSummary(w io.Writer, summary api.DeploymentSummaryResponse, appSlug string) {
	_, _ = fmt.Fprintln(w, "Release summary:")
	switch {
	case summary.Previous == nil:
		_, _ = fmt.Fprintln(w, "  Changes: initial release")
	case len(summary.Changes) == 0:
		_, _ = fmt.Fprintf(w, "  Changes since %s: none\n", summary.Previous.ID)
	default:
		_, _ = fmt.Fprintf(w, "  Changes since %s:\n", summary.Previous.ID)
		for _, change := range summary.Changes {
			_, _ = fmt.Fprintf(w, "    %-18s %s -> %s\n", change.Field,
				formatSummaryValue(change.Before), formatSummaryValue(change.After))
		}
	}
	if summary.RollbackTargetID == "" {
		_, _ = fmt.Fprintln(w, "  Rollback: unavailable (no previous release)")
		return
	}
	_, _ = fmt.Fprintf(w, "  Rollback: gregale rollback %s --to %s\n", appSlug, summary.RollbackTargetID)
}

// waitForDeploymentReceiptUntil is the timeout-aware implementation used by
// JSON deploys whenever lifecycle waiting is enabled (the default).
func waitForDeploymentReceiptUntil(ctx context.Context, c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.DeploymentResponse, bool) {
	if dep.Status == statusLive || dep.Status == deploymentStatusFailed {
		return deploymentWithReceipt(ctx, c, dep), true
	}
	if c == nil {
		return api.DeploymentResponse{}, false
	}
	return pollDeploymentFinalUntilContext(ctx, c, dep, deadline)
}

// writeWaitedDeploymentReceiptUntil emits a single terminal-or-timeout JSON
// object using the caller's wait deadline. A timeout still returns the
// accepted deployment id so automation can resume with `deployment wait`.
func writeWaitedDeploymentReceiptUntil(ctx context.Context, c *Client, dep api.DeploymentResponse, prov *zeroConfigProvenance, appURL, sourceSHA256, appSlug string, deadline time.Duration) int {
	final, ok := waitForDeploymentReceiptUntil(ctx, c, dep, deadline)
	if !ok {
		PrintWarn(osStderr, "deployment did not reach a terminal state before the wait deadline; deployment=%s", dep.ID)
		if code := jsonOut(writeJSON(newDeployReceipt(dep, prov, appURL, sourceSHA256))); code != 0 {
			return code
		}
		return 3
	}
	receipt := newDeployReceipt(final, prov, appURL, sourceSHA256)
	if final.Status == statusLive {
		if summary, summaryOK := deploymentWithReleaseSummary(ctx, c, appSlug, final.ID); summaryOK {
			receipt.ReleaseSummary = newDeployReleaseSummary(summary, appSlug)
		}
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if final.Status == deploymentStatusFailed {
		return 1
	}
	return 0
}
