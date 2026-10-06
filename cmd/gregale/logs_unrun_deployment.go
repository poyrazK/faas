package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// runtime path still runs.
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
	PrintWarn(osStderr, "deployment %s is %s and never served; showing its build and deploy log", dep.ID, dep.Status)
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
				_, _ = fmt.Fprintln(osStdout, entry.Line)
			}
		case "end":
			return finishUnrunDeploymentLogs(ctx, client, dep), true
		}
	}
	return finishUnrunDeploymentLogs(ctx, client, dep), true
}

// finishUnrunDeploymentLogs ends the log with the deployment's persisted
// failure reason, which the log rows alone may not state.
func finishUnrunDeploymentLogs(ctx context.Context, client *api.Client, dep api.DeploymentResponse) int {
	if fresh, err := client.GetDeployment(ctx, dep.ID); err == nil {
		dep = fresh
	}
	if dep.Status == deploymentStatusFailed && dep.Error != "" {
		PrintWarn(osStderr, "deployment %s failed: %s", dep.ID, dep.Error)
	}
	return 0
}
