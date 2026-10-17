package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func runAutomationLive(ctx context.Context, client *api.Client, o automationLiveOptions) int {
	if o.verb == "list" {
		return listAutomationLive(ctx, client, o)
	}
	requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
	var view api.AutomationLiveVerification
	var err error
	if o.verb == "start" {
		view, err = client.StartAutomationLiveVerification(requestCtx, o.app, o.name, o.start, o.key)
	} else {
		view, err = client.GetAutomationLiveVerification(requestCtx, o.app, o.name, o.run)
	}
	cancel()
	if err != nil {
		return automationLiveError(ctx, o, nil, err)
	}
	if !validAutomationLiveView(view, o.run) || !automationLiveStartMatches(o, view) {
		return automationLiveProtocolError(o, nil)
	}
	if ctx.Err() != nil {
		return automationLiveError(ctx, o, &view, ctx.Err())
	}
	if o.wait {
		return waitAutomationLive(ctx, client, o, view)
	}
	switch o.verb {
	case "start":
		return renderAutomationLive(o, &view, "accepted", "Live verification accepted. This confirms admission; use live wait to obtain an assertion verdict.", 0)
	case "show":
		return renderAutomationLive(o, &view, "observed", "Live verification observed. Use live check or live wait to require a current passing verdict.", 0)
	default:
		return checkAutomationLive(ctx, client, o, view)
	}
}

func automationLiveStartMatches(o automationLiveOptions, view api.AutomationLiveVerification) bool {
	if o.verb != "start" {
		return true
	}
	deployment, _ := uuid.Parse(view.DeploymentID)
	expected, _ := uuid.Parse(o.start.ExpectedDeploymentID)
	return view.CaseName == o.start.CaseName && view.Version == o.start.ExpectedVersion && view.PublishedVersion == o.start.ExpectedPublishedVersion && deployment == expected
}

func validAutomationLiveView(view api.AutomationLiveVerification, runID string) bool {
	if !automationLiveUUID(view.RunID) || !automationLiveUUID(view.DeploymentID) || !automationLiveName(view.CaseName, api.AutomationTestCaseNameMaxBytes) || view.Version <= 0 || view.PublishedVersion <= 0 || view.ReportCurrent && view.Report == nil {
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
		return !view.ReportCurrent || view.Report.RunStatus == view.RunStatus && terminalAutomationLive(view.RunStatus)
	default:
		return false
	}
}

func terminalAutomationLive(state string) bool {
	return state == "succeeded" || state == "failed" || state == "dead"
}

func automationLiveVerdict(view api.AutomationLiveVerification) (string, string, int) {
	if view.Outdated {
		return "outdated", "This verification describes an older case, published revision or deployment. Verify the current target explicitly.", 1
	}
	if !terminalAutomationLive(view.RunStatus) {
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

func checkAutomationLive(ctx context.Context, client *api.Client, o automationLiveOptions, view api.AutomationLiveVerification) int {
	if !view.Outdated && terminalAutomationLive(view.RunStatus) && !view.ReportCurrent {
		checked, err := requestAutomationLiveCheck(ctx, client, o, view.RunID)
		if err != nil {
			return automationLiveError(ctx, o, &view, err)
		}
		if !validAutomationLiveView(checked, view.RunID) {
			return automationLiveProtocolError(o, &view)
		}
		view = checked
	}
	if ctx.Err() != nil {
		return automationLiveError(ctx, o, &view, ctx.Err())
	}
	outcome, message, code := automationLiveVerdict(view)
	return renderAutomationLive(o, &view, outcome, message, code)
}

func requestAutomationLiveCheck(ctx context.Context, client *api.Client, o automationLiveOptions, run string) (api.AutomationLiveVerification, error) {
	requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	return client.CheckAutomationLiveVerification(requestCtx, o.app, o.name, run)
}

func waitAutomationLive(ctx context.Context, client *api.Client, o automationLiveOptions, view api.AutomationLiveVerification) int {
	previous := ""
	for ctx.Err() == nil {
		if view.Outdated || terminalAutomationLive(view.RunStatus) {
			if !view.Outdated && !view.ReportCurrent {
				checked, err := requestAutomationLiveCheck(ctx, client, o, view.RunID)
				if err != nil {
					return automationLiveError(ctx, o, &view, err)
				}
				if !validAutomationLiveView(checked, view.RunID) {
					return automationLiveProtocolError(o, &view)
				}
				view = checked
			}
			if view.Outdated || terminalAutomationLive(view.RunStatus) {
				if ctx.Err() != nil {
					return automationLiveError(ctx, o, &view, ctx.Err())
				}
				outcome, message, code := automationLiveVerdict(view)
				return renderAutomationLive(o, &view, outcome, message, code)
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
			return automationLiveError(ctx, o, &view, ctx.Err())
		case <-timer.C:
		}
		requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
		next, err := client.GetAutomationLiveVerification(requestCtx, o.app, o.name, view.RunID)
		cancel()
		if err != nil {
			return automationLiveError(ctx, o, &view, err)
		}
		if !validAutomationLiveView(next, view.RunID) {
			return automationLiveProtocolError(o, &view)
		}
		view = next
	}
	return automationLiveError(ctx, o, &view, ctx.Err())
}

func automationLiveError(ctx context.Context, o automationLiveOptions, view *api.AutomationLiveVerification, err error) int {
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
	return failAutomationLive(o, view, outcome, message, code, status)
}

func automationLiveProtocolError(o automationLiveOptions, view *api.AutomationLiveVerification) int {
	return failAutomationLive(o, view, "error", "The response did not identify a valid verification for the requested target. Reconnect to the known run; verify any unconfirmed admission with the original start flags and idempotency key.", 3, 502)
}

func failAutomationLive(o automationLiveOptions, view *api.AutomationLiveVerification, outcome, message string, code, status int) int {
	problem := api.Problem{Status: status, Code: "live_verification_" + outcome, Title: "Live verification observation stopped", Detail: message, DocsURL: cliDocsURL}
	if jsonOutput {
		if err := writeJSONProblemWithExit(osStderr, problem, code); err != nil {
			return printErr("Could not write live verification error", err)
		}
	} else {
		renderAPIError(osStderr, &api.APIError{Problem: problem})
	}
	return renderAutomationLive(o, view, outcome, message, code)
}

func listAutomationLive(ctx context.Context, client *api.Client, o automationLiveOptions) int {
	requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	listing, err := client.ListAutomationLiveVerifications(requestCtx, o.app, o.name)
	if err != nil {
		return automationLiveError(ctx, o, nil, err)
	}
	for _, view := range listing.Verifications {
		if !validAutomationLiveView(view, "") {
			return automationLiveProtocolError(o, nil)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App  string `json:"app"`
			Name string `json:"name"`
			api.ListAutomationLiveVerificationsResponse
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
