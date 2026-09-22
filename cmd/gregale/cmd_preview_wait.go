package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// previewWaitPollInterval is a package variable so the hermetic command
// tests can exercise a queued-to-ready transition without sleeping seconds.
var previewWaitPollInterval = 2 * time.Second

type previewWaitReceipt struct {
	Preview       previewSummary `json:"preview"`
	Deployment    *DeployReceipt `json:"deployment,omitempty"`
	Ready         bool           `json:"ready"`
	TimedOut      bool           `json:"timed_out,omitempty"`
	ResumeCommand string         `json:"resume_command,omitempty"`
	NextAction    string         `json:"next_action,omitempty"`
}

// cmdPreviewWait waits for the newest deployment belonging to a preview app.
// It deliberately resolves the deployment by preview slug instead of asking
// callers to copy an internal deployment id from create output. A new push can
// replace that deployment while the command is waiting, so each poll reads
// the app-scoped latest deployment endpoint.
func cmdPreviewWait(args []string) int {
	flags, pos := splitArgsForFlags(args, "progress", "open")
	fs := newFlagSet("preview wait", flag.ContinueOnError)
	progress := fs.Bool("progress", false, "print deployment transitions while waiting (human output only)")
	openURL := fs.Bool("open", false, "open the preview URL after it becomes ready")
	timeoutSeconds := fs.Int("timeout", defaultDeployWaitTimeoutSeconds, "maximum seconds to wait for preview readiness")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || !validCLISlug(pos[0]) || *timeoutSeconds <= 0 || *timeoutSeconds > 24*60*60 {
		PrintUsage(osStderr, "usage: gregale preview wait <preview-slug> [--progress] [--open] [--timeout SECONDS]", "preview")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	waitTimeout := time.Duration(*timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if !jsonOutput {
		PrintProgress(osStdout, "Waiting for preview %s to become ready...", pos[0])
	}

	var (
		lastState     api.PreviewStatusResponse
		haveLastState bool
		progressState *deploymentProgressSnapshot
	)
	for {
		state, getErr := client.GetPreviewStatus(ctx, pos[0])
		if getErr != nil {
			if haveLastState && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return renderPreviewWaitTimeout(lastState, waitTimeout)
			}
			return printErr("Could not load preview", getErr)
		}
		lastState = state
		haveLastState = true
		if *progress && !jsonOutput && state.LatestDeployment != nil {
			progressState = renderDeploymentProgress(osStdout, *state.LatestDeployment, progressState)
		}

		if state.App.PreviewPRState == "torn_down" {
			return renderPreviewWaitTerminal(state, client, false, false)
		}
		if state.LatestDeployment != nil && isCompletedDeployment(*state.LatestDeployment) {
			return renderPreviewWaitTerminal(state, client, state.LatestDeployment.Status == statusLive, *openURL)
		}

		timer := time.NewTimer(previewWaitPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return renderPreviewWaitTimeout(lastState, waitTimeout)
		case <-timer.C:
		}
	}
}

func renderPreviewWaitTimeout(state api.PreviewStatusResponse, timeout time.Duration) int {
	receipt := newPreviewWaitReceipt(state, false)
	receipt.TimedOut = true
	receipt.ResumeCommand = fmt.Sprintf("gregale preview wait %s --timeout %d", state.App.Slug, int(timeout/time.Second))
	if receipt.Deployment == nil {
		receipt.NextAction = fmt.Sprintf("gregale preview show %s", state.App.Slug)
	} else {
		receipt.NextAction = fmt.Sprintf("gregale logs %s --deployment %s --follow", state.App.Slug, receipt.Deployment.ID)
	}
	if jsonOutput {
		PrintWarn(osStderr, "preview did not become ready before the wait deadline; resume with: %s", receipt.ResumeCommand)
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
		return 3
	}
	PrintWarn(osStderr, "preview %s did not become ready after %s; resume with: %s", state.App.Slug, timeout, receipt.ResumeCommand)
	PrintProgress(osStderr, "next: %s", receipt.NextAction)
	return 3
}

func renderPreviewWaitTerminal(state api.PreviewStatusResponse, client *Client, ready, open bool) int {
	var final *api.DeploymentResponse
	if state.LatestDeployment != nil {
		dep := deploymentWithReceipt(context.Background(), client, *state.LatestDeployment)
		final = &dep
		state.LatestDeployment = &dep
	}
	receipt := newPreviewWaitReceipt(state, ready)
	if !ready {
		if final != nil {
			receipt.NextAction = fmt.Sprintf("gregale logs %s --deployment %s --follow", state.App.Slug, final.ID)
		} else {
			receipt.NextAction = "create a new preview from the pull request"
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
		if !ready {
			return 1
		}
		return 0
	}
	if !ready {
		if final != nil && final.Status == deploymentStatusFailed {
			code := renderDeployFailure(*final)
			PrintProgress(osStderr, "follow: gregale logs %s --deployment %s --follow", state.App.Slug, final.ID)
			return code
		}
		PrintFail(osStderr, "Preview %s is not ready (%s).", state.App.Slug, valueOrDash(state.App.PreviewPRState))
		PrintProgress(osStderr, "next: %s", receipt.NextAction)
		return 1
	}
	PrintOK(osStdout, "Preview ready: %s", previewURLFromApp(state.App))
	PrintProgress(osStdout, "Deployment: %s", final.ID)
	PrintProgress(osStdout, "Expires: %s", previewExpiry(state.App.PreviewExpiresAt))
	openPreviewURL(open, previewURLFromApp(state.App))
	return 0
}

func newPreviewWaitReceipt(state api.PreviewStatusResponse, ready bool) previewWaitReceipt {
	var dep *DeployReceipt
	if state.LatestDeployment != nil {
		dep = newDeployReceipt(*state.LatestDeployment, nil, previewURLFromApp(state.App), "")
	}
	return previewWaitReceipt{
		Preview:    previewSummaryFromApp(state.App, state.LatestDeployment),
		Deployment: dep,
		Ready:      ready,
	}
}

func previewURLFromApp(app api.AppResponse) string {
	return canonicalAppURL(app)
}
