package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildLogsForUnrunDeployment serves `gregale logs <slug> --deployment <id>`
// for a deployment that never started an instance. Deploy prints that
// command when a build fails, but runtime logs need a running (or archived)
// instance. On production-us it answered "No running instance is available"
// for the exact command the failure told the customer to run. A failed,
// cancelled or still-building deployment now streams its build/deploy log
// (GET /v1/deployments/{id}/logs). handled is false for deployments that can
// have runtime logs, for an unknown status, and on a lookup error, so the
// runtime path still runs. Under --json each entry is one JSON log record on
// stdout and the stream ends with a summary record carrying the failure: a
// failed deploy's recovery command points here, and AGENTS.md has agents run
// every command with --json (#6635).
func buildLogsForUnrunDeployment(ctx context.Context, client *api.Client, deploymentID string, follow bool) (code int, handled bool) {
	dep, err := client.GetDeployment(ctx, deploymentID)
	if err != nil {
		return 0, false
	}
	switch dep.Status {
	case deploymentStatusFailed, "cancelled", "pending", "building", "imaging", "snapshotting":
	default:
		// live, superseded, or unknown: runtime (or archived) logs apply.
		return 0, false
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if !jsonOutput {
		PrintWarn(osStderr, "deployment %s is %s and never served; showing its build and deploy log", dep.ID, dep.Status)
	}
	body, err := client.StreamDeploymentLogs(ctx, dep.ID, nil, 500, follow && !isTerminalDeploymentStatus(dep.Status))
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			renderAPIError(os.Stderr, ae)
			return exitCodeForStatus(ae.Problem.Status), true
		}
		return printErr("Could not reach the API", err), true
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	for e := range dec.Events() {
		switch e.Event {
		case "log":
			var entry struct {
				Line string `json:"line"`
			}
			if json.Unmarshal([]byte(e.Data), &entry) == nil && entry.Line != "" {
				if jsonOutput {
					_ = json.NewEncoder(osStdout).Encode(unrunDeploymentLogRecord{Type: "log", Deployment: dep.ID, Line: entry.Line})
				} else {
					_ = printTerminalSafeLines(osStdout, "", entry.Line)
				}
			}
		case "end":
			return finishUnrunDeploymentLogs(ctx, client, dep), true
		}
	}
	return finishUnrunDeploymentLogs(ctx, client, dep), true
}

// unrunDeploymentLogRecord is one --json record of a never-served
// deployment's build/deploy log: a "log" line, then one "summary".
type unrunDeploymentLogRecord struct {
	Type       string `json:"type"`
	Deployment string `json:"deployment_id"`
	Line       string `json:"line,omitempty"`
	Status     string `json:"status,omitempty"`
	Source     string `json:"source,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	ErrorHint  string `json:"error_hint,omitempty"`
	Message    string `json:"message,omitempty"`
}

// finishUnrunDeploymentLogs ends the log with the deployment's persisted
// failure reason, which the log rows alone may not state.
func finishUnrunDeploymentLogs(ctx context.Context, client *api.Client, dep api.DeploymentResponse) int {
	if fresh, err := client.GetDeployment(ctx, dep.ID); err == nil {
		dep = fresh
	}
	if jsonOutput {
		message := "This deployment never served, so it has build and deploy logs only; runtime and HTTP logs are separate."
		if !isTerminalDeploymentStatus(dep.Status) {
			message = "This deployment has not served yet, so it has build and deploy logs only; runtime and HTTP logs start once it serves."
		}
		return jsonOut(json.NewEncoder(osStdout).Encode(unrunDeploymentLogRecord{
			Type: "summary", Deployment: dep.ID, Status: dep.Status, Source: "build/deploy",
			Error: dep.Error, ErrorCode: dep.ErrorCode, ErrorHint: dep.ErrorHint, Message: message,
		}))
	}
	if dep.Status == deploymentStatusFailed && dep.Error != "" {
		PrintWarn(osStderr, "deployment %s failed: %s", dep.ID, dep.Error)
	}
	return 0
}
