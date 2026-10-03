package main

// Deployment-attached one-off commands (ADR-230).
//
// `gregale app <slug> exec -- <command> [args...]` selects the app's live
// deployment on the server, runs the command in a fresh task VM, and waits for
// its bounded terminal output by default. `--detach` returns the durable task
// receipt immediately. Shell interpretation is never implicit.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const appTaskWaitTimeoutDefault = 65 * time.Minute

func cmdAppExec(slug string, args []string) int {
	fs := newFlagSet("app-exec", flag.ContinueOnError)
	shell := fs.Bool("shell", false, "interpret one command string through the app shell")
	detach := fs.Bool("detach", false, "return after the task is queued")
	timeoutSeconds := fs.Int("timeout-seconds", 0, "server-side command timeout in seconds (1..3600; 0 uses the default)")
	maxOutputBytes := fs.Int("max-output-bytes", 0, "combined stdout/stderr tail cap (1024..16777216; 0 uses the default)")
	operationPolicy := fs.String("operation-policy", "", "route this command through a managed exclusive-operation policy")
	operationKey := fs.String("operation-key", "", "JSON scalar business coordination key (requires --operation-policy)")
	equivalenceKey := fs.String("equivalence-key", "", "equivalent request identity for join_existing policies")
	idempotencyKey := fs.String("idempotency-key", "", "stable retry identity for this submission")
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval while attached")
	waitTimeout := fs.Duration("wait-timeout", appTaskWaitTimeoutDefault, "maximum time for the CLI to remain attached")
	flags, positionals := splitArgsForFlags(args, "shell", "detach")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *shell && len(positionals) != 1 {
		printAppExecUsage()
		return 1
	}
	if slug == "" || len(positionals) == 0 || *pollInterval <= 0 || *waitTimeout <= 0 {
		printAppExecUsage()
		return 1
	}
	if (*operationPolicy == "") != (*operationKey == "") || (*operationKey != "" && !validJSONScalar(*operationKey)) {
		printAppExecUsage()
		return 1
	}
	request := api.CreateAppTaskRequest{
		Command:        positionals,
		CommandShell:   *shell,
		TimeoutSeconds: *timeoutSeconds,
		MaxOutputBytes: *maxOutputBytes,
	}
	if _, problem := request.Resolve(); problem != nil {
		return printErr("Invalid app command", problem)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	managed := *operationPolicy != ""
	var task api.AppTaskResponse
	var operationID string
	if managed {
		accepted, submitErr := client.SubmitExclusiveAppTaskOperation(context.Background(), slug, api.ExclusiveAppTaskOperationRequest{
			Policy: *operationPolicy, Key: json.RawMessage(*operationKey), EquivalenceKey: *equivalenceKey, Task: request,
		}, *idempotencyKey)
		if submitErr != nil {
			return printErr("Managed app command submission failed", submitErr)
		}
		operationID = accepted.ID
		if *detach {
			if jsonOutput {
				return jsonOut(writeJSON(accepted))
			}
			PrintOK(osStdout, "Operation %s accepted (%s).", accepted.ID, accepted.StatusURL)
			return 0
		}
	} else {
		var submitErr error
		task, submitErr = client.CreateAppTask(context.Background(), slug, request)
		if submitErr != nil {
			return printErr("App command submission failed", submitErr)
		}
		if *detach {
			if jsonOutput {
				return jsonOut(writeJSON(task))
			}
			PrintOK(osStdout, "Task %s queued for %s (deployment=%s).", task.ID, slug, task.DeploymentID)
			return 0
		}
	}

	interruptContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, *waitTimeout)
	defer cancel()
	for (managed && operationID != "") || (!managed && !task.Status.Terminal()) {
		select {
		case <-waitContext.Done():
			if errors.Is(interruptContext.Err(), context.Canceled) {
				if managed {
					if cancelErr := client.CancelExclusiveOperation(context.Background(), operationID); cancelErr != nil {
						PrintWarn(osStderr, "could not request cancellation for operation %s: %v", operationID, cancelErr)
					} else if !jsonOutput {
						PrintWarn(osStderr, "cancellation requested for operation %s", operationID)
					}
				} else {
					cancelled, cancelErr := client.CancelAppTask(context.Background(), slug, task.ID)
					if cancelErr != nil {
						PrintWarn(osStderr, "could not request cancellation for task %s: %v", task.ID, cancelErr)
					} else if !jsonOutput {
						PrintWarn(osStderr, "cancellation requested for task %s (status=%s)", task.ID, cancelled.Status)
					}
				}
				return 130
			}
			return printErr("App command wait timed out; the task is still running", waitContext.Err())
		case <-time.After(*pollInterval):
		}
		if managed {
			operation, readErr := client.GetExclusiveOperation(waitContext, operationID)
			if readErr != nil {
				if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
					return printErr("App command wait timed out; the operation is still running", waitContext.Err())
				}
				return printErr("Managed app command status failed", readErr)
			}
			if operation.State == "failed" {
				PrintFail(osStderr, "Managed app command failed: %s", operation.LastError)
				return 1
			}
			if operation.State == "cancelled" {
				PrintFail(osStderr, "Managed app command was cancelled.")
				return 130
			}
			if operation.State == "completed" {
				var result struct {
					AppTaskID string `json:"app_task_id"`
				}
				if err := json.Unmarshal(operation.Result, &result); err != nil || result.AppTaskID == "" {
					return printErr("Managed app command result is unavailable", errors.New("operation receipt has no app_task_id"))
				}
				task, err = client.GetAppTask(waitContext, slug, result.AppTaskID)
				if err != nil {
					return printErr("Managed app task result failed", err)
				}
				operationID = ""
			}
		} else {
			task, err = client.GetAppTask(waitContext, slug, task.ID)
			if err != nil {
				if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
					return printErr("App command wait timed out; the task is still running", waitContext.Err())
				}
				return printErr("App command status failed", err)
			}
		}
	}
	if jsonOutput {
		if err := writeJSON(task); err != nil {
			return jsonOut(err)
		}
		return appTaskExitCode(task)
	}
	return renderAppTaskTerminal(task)
}

func validJSONScalar(value string) bool {
	var scalar any
	if err := json.Unmarshal([]byte(value), &scalar); err != nil || scalar == nil {
		return false
	}
	switch scalar.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

func printAppExecUsage() {
	PrintUsage(
		osStderr,
		"usage: gregale app <slug> exec [--shell] [--detach] [--timeout-seconds N] [--max-output-bytes N] [--operation-policy NAME --operation-key JSON] [--equivalence-key KEY] [--idempotency-key KEY] [--poll-interval D] [--wait-timeout D] -- <command> [args...]",
		"apps",
	)
}

func renderAppTaskTerminal(task api.AppTaskResponse) int {
	if task.Status == api.AppTaskStatusSucceeded {
		PrintOK(osStdout, "Task %s succeeded (deployment=%s).", task.ID, task.DeploymentID)
	} else {
		PrintFail(osStderr, "Task %s finished with status=%s.", task.ID, task.Status)
	}
	if task.StdoutTail != "" {
		_, _ = fmt.Fprint(osStdout, task.StdoutTail)
	}
	if task.StderrTail != "" {
		_, _ = fmt.Fprint(osStderr, task.StderrTail)
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "task output was truncated at %d bytes", task.MaxOutputBytes)
	}
	if task.Failure != nil {
		PrintFail(osStderr, "%s: %s", task.Failure.Code, task.Failure.Message)
	}
	return appTaskExitCode(task)
}

func appTaskExitCode(task api.AppTaskResponse) int {
	if task.Status == api.AppTaskStatusSucceeded {
		return 0
	}
	if task.ExitCode != nil && *task.ExitCode > 0 && *task.ExitCode <= 255 {
		return *task.ExitCode
	}
	if task.Status == api.AppTaskStatusTimedOut {
		return 124
	}
	return 1
}
