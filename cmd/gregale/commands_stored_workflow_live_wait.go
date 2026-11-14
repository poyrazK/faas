package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func runStoredWorkflowLive(ctx context.Context, client *api.Client, o storedWorkflowLiveOptions) int {
	if o.verb == "list" {
		return listStoredWorkflowLive(ctx, client, o)
	}
	requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	var view api.StoredWorkflowLiveVerification
	var err error
	if o.verb == "start" {
		view, err = client.StartStoredWorkflowLiveVerification(requestCtx, o.app, o.name, o.start, o.key)
	} else {
		view, err = client.GetStoredWorkflowLiveVerification(requestCtx, o.app, o.name, o.run)
	}
	cancel()
	if err != nil {
		return storedWorkflowLiveError(ctx, o, nil, err)
	}
	if !validStoredWorkflowLiveView(view, o.run) || !storedWorkflowLiveStartMatches(o, view) {
		return storedWorkflowLiveProtocolError(o, nil)
	}
	if ctx.Err() != nil {
		return storedWorkflowLiveError(ctx, o, &view, ctx.Err())
	}
	if o.wait {
		return waitStoredWorkflowLive(ctx, client, o, view)
	}
	switch o.verb {
	case "start":
		return renderStoredWorkflowLive(o, &view, "accepted", "Live verification accepted. This confirms admission; use live wait to obtain an assertion verdict.", 0)
	case "show":
		return renderStoredWorkflowLive(o, &view, "observed", "Live verification observed. Use live check or live wait to require a current passing verdict.", 0)
	default:
		return checkStoredWorkflowLive(ctx, client, o, view)
	}
}

func storedWorkflowLiveStartMatches(o storedWorkflowLiveOptions, view api.StoredWorkflowLiveVerification) bool {
	if o.verb != "start" {
		return true
	}
	deployment, _ := uuid.Parse(view.DeploymentID)
	expected, _ := uuid.Parse(o.start.ExpectedDeploymentID)
	return view.CaseName == o.start.CaseName && view.Version == o.start.ExpectedVersion && view.PublishedVersion == o.start.ExpectedPublishedVersion && deployment == expected
}

func validStoredWorkflowLiveView(view api.StoredWorkflowLiveVerification, runID string) bool {
	if !storedWorkflowLiveUUID(view.RunID) || !storedWorkflowLiveUUID(view.DeploymentID) || !storedWorkflowLiveName(view.CaseName, api.StoredWorkflowTestCaseNameMaxBytes) || view.Version <= 0 || view.PublishedVersion <= 0 || view.ReportCurrent && view.Report == nil {
		return false
	}
	if runID != "" {
		expected, err := uuid.Parse(runID)
		actual, _ := uuid.Parse(view.RunID)
		if err != nil || expected != actual {
			return false
		}
	}
	switch view.RunStatus {
	case "pending", "running", "awaiting_event", "succeeded", "failed", "dead":
		return !view.ReportCurrent || view.Report.RunStatus == view.RunStatus && terminalStoredWorkflowLive(view.RunStatus)
	default:
		return false
	}
}

func terminalStoredWorkflowLive(state string) bool {
	return state == "succeeded" || state == "failed" || state == "dead"
}

func storedWorkflowLiveVerdict(view api.StoredWorkflowLiveVerification) (string, string, int) {
	if view.Outdated {
		return "outdated", "This verification describes an older case, published revision or deployment. Verify the current target explicitly.", 1
	}
	if !terminalStoredWorkflowLive(view.RunStatus) {
		return "pending", "Execution is still queued, running or waiting. No assertion verdict is available yet.", 1
	}
	if view.Report == nil || !view.ReportCurrent {
		return "unavailable", "The assertion report is absent or the run changed during checking. Reconnect to inspect it again.", 1
	}
	if !view.Report.Passed {
		return "failed", "Live verification failed its frozen assertions. Inspect the safe mismatch fields in its report.", 1
	}
	return "passed", "Live verification passed for the current target.", 0
}

func checkStoredWorkflowLive(ctx context.Context, client *api.Client, o storedWorkflowLiveOptions, view api.StoredWorkflowLiveVerification) int {
	if !view.Outdated && terminalStoredWorkflowLive(view.RunStatus) && !view.ReportCurrent {
		checked, err := requestStoredWorkflowLiveCheck(ctx, client, o, view.RunID)
		if err != nil {
			return storedWorkflowLiveError(ctx, o, &view, err)
		}
		if !validStoredWorkflowLiveView(checked, view.RunID) {
			return storedWorkflowLiveProtocolError(o, &view)
		}
		view = checked
	}
	if ctx.Err() != nil {
		return storedWorkflowLiveError(ctx, o, &view, ctx.Err())
	}
	outcome, message, code := storedWorkflowLiveVerdict(view)
	return renderStoredWorkflowLive(o, &view, outcome, message, code)
}

func requestStoredWorkflowLiveCheck(ctx context.Context, client *api.Client, o storedWorkflowLiveOptions, run string) (api.StoredWorkflowLiveVerification, error) {
	requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	defer cancel()
	return client.CheckStoredWorkflowLiveVerification(requestCtx, o.app, o.name, run)
}

func waitStoredWorkflowLive(ctx context.Context, client *api.Client, o storedWorkflowLiveOptions, view api.StoredWorkflowLiveVerification) int {
	previous := ""
	for ctx.Err() == nil {
		if view.Outdated || terminalStoredWorkflowLive(view.RunStatus) {
			if !view.Outdated && !view.ReportCurrent {
				checked, err := requestStoredWorkflowLiveCheck(ctx, client, o, view.RunID)
				if err != nil {
					return storedWorkflowLiveError(ctx, o, &view, err)
				}
				if !validStoredWorkflowLiveView(checked, view.RunID) {
					return storedWorkflowLiveProtocolError(o, &view)
				}
				view = checked
			}
			if view.Outdated || terminalStoredWorkflowLive(view.RunStatus) {
				if ctx.Err() != nil {
					return storedWorkflowLiveError(ctx, o, &view, ctx.Err())
				}
				outcome, message, code := storedWorkflowLiveVerdict(view)
				return renderStoredWorkflowLive(o, &view, outcome, message, code)
			}
		}
		if !jsonOutput && view.RunStatus != previous {
			if _, err := fmt.Fprintf(osStderr, "Live verification %s: %s\n", view.RunID, view.RunStatus); err != nil {
				return printErr("Could not write live verification progress", err)
			}
		}
		previous = view.RunStatus
		timer := time.NewTimer(o.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return storedWorkflowLiveError(ctx, o, &view, ctx.Err())
		case <-timer.C:
		}
		requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
		next, err := client.GetStoredWorkflowLiveVerification(requestCtx, o.app, o.name, view.RunID)
		cancel()
		if err != nil {
			return storedWorkflowLiveError(ctx, o, &view, err)
		}
		if !validStoredWorkflowLiveView(next, view.RunID) {
			return storedWorkflowLiveProtocolError(o, &view)
		}
		view = next
	}
	return storedWorkflowLiveError(ctx, o, &view, ctx.Err())
}

func storedWorkflowLiveError(ctx context.Context, o storedWorkflowLiveOptions, view *api.StoredWorkflowLiveVerification, err error) int {
	outcome, code, status := "error", 3, 503
	message := "Live verification request failed. Reconnect using the known run ID; if start admission was not confirmed, retry only the original start flags and idempotency key."
	if ctx.Err() != nil {
		outcome, code, status = "timed_out", 124, 408
		message = "Live verification waiting timed out. Execution may continue; reconnect using its run ID. If admission was not confirmed, reuse the original start flags and idempotency key."
		if errors.Is(ctx.Err(), context.Canceled) {
			outcome, code, message = "interrupted", 130, "Live verification observation was interrupted. Execution may continue; reconnect using its run ID or reuse the original start flags and idempotency key if admission was not confirmed."
			status = 499
		}
	} else {
		var failure *api.APIError
		if errors.As(err, &failure) {
			code = exitCodeForStatus(failure.Problem.Status)
			status = failure.Problem.Status
		}
	}
	return failStoredWorkflowLive(o, view, outcome, message, code, status)
}

func storedWorkflowLiveProtocolError(o storedWorkflowLiveOptions, view *api.StoredWorkflowLiveVerification) int {
	return failStoredWorkflowLive(o, view, "error", "The response did not identify a valid verification for the requested target. Reconnect to the known run; verify any unconfirmed admission with the original start flags and idempotency key.", 3, 502)
}

func failStoredWorkflowLive(o storedWorkflowLiveOptions, view *api.StoredWorkflowLiveVerification, outcome, message string, code, status int) int {
	problem := api.Problem{Status: status, Code: "live_verification_" + outcome, Title: "Live verification observation stopped", Detail: message, DocsURL: cliDocsURL}
	if jsonOutput {
		if err := writeJSONProblemWithExit(osStderr, problem, code); err != nil {
			return printErr("Could not write live verification error", err)
		}
	} else {
		renderAPIError(osStderr, &api.APIError{Problem: problem})
	}
	return renderStoredWorkflowLive(o, view, outcome, message, code)
}

func listStoredWorkflowLive(ctx context.Context, client *api.Client, o storedWorkflowLiveOptions) int {
	requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	defer cancel()
	listing, err := client.ListStoredWorkflowLiveVerifications(requestCtx, o.app, o.name)
	if err != nil {
		return storedWorkflowLiveError(ctx, o, nil, err)
	}
	for _, view := range listing.Verifications {
		if !validStoredWorkflowLiveView(view, "") {
			return storedWorkflowLiveProtocolError(o, nil)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App  string `json:"app"`
			Name string `json:"name"`
			api.ListStoredWorkflowLiveVerificationsResponse
		}{o.app, o.name, listing}))
	}
	if _, err := fmt.Fprintf(osStdout, "Published revision: %d\nCurrent deployment: %s\n", listing.PublishedVersion, listing.DeploymentID); err != nil {
		return printErr("Could not write live verification list", err)
	}
	for _, view := range listing.Verifications {
		if _, err := fmt.Fprintf(osStdout, "%s  %s  %s  outdated=%t report_current=%t\n", view.RunID, view.CaseName, view.RunStatus, view.Outdated, view.ReportCurrent); err != nil {
			return printErr("Could not write live verification list", err)
		}
	}
	return 0
}
