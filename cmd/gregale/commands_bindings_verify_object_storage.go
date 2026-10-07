package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runObjectStorageBindingProbe(ctx context.Context, client bindingInventoryProbeClient, slug, prefix string, pollInterval, waitTimeout time.Duration) int {
	inventory, err := client.GetAppBindingInventory(ctx, slug, "")
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	bound := false
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeObjectStorage && item.Binding == prefix && item.Scope == inventory.VerificationScope {
			bound = true
			break
		}
	}
	if !bound {
		return printErr("Object-storage prefix is not bound in the selected live scope", fmt.Errorf("%s has no selected managed object-storage binding for %s", slug, prefix))
	}

	report, task, errorTitle, exitCode, err := executeObjectStorageBindingProbe(ctx, client, slug, prefix, pollInterval, waitTimeout)
	if errorTitle != "" {
		return printErr(errorTitle, err)
	}
	if exitCode == 130 {
		return exitCode
	}
	if exitCode == 0 && bindingProbeDeploymentChanged(inventory, task) {
		report.Error = "live deployment changed during verification; run verification again"
		exitCode = 1
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return jsonOut(err)
		}
		return exitCode
	}
	renderObjectStorageBindingProbeReport(slug, report, task)
	return exitCode
}

func executeObjectStorageBindingProbe(ctx context.Context, client serviceBindingProbeClient, slug, prefix string, pollInterval, waitTimeout time.Duration) (api.ObjectStorageBindingProbeReport, api.AppTaskResponse, string, int, error) {
	report := newObjectStorageBindingProbeReport(slug, prefix)
	task, err := client.CreateAppTask(ctx, slug, api.CreateAppTaskRequest{
		Command:        []string{api.AppTaskObjectStorageBindingProbeCommand, prefix},
		TimeoutSeconds: bindingProbeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
	})
	if err != nil {
		report.Error = err.Error()
		return report, task, "Could not start Object-storage binding canary", 1, err
	}
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, waitTimeout)
	defer cancel()
	for !task.Status.Terminal() {
		select {
		case <-waitContext.Done():
			if errors.Is(interruptContext.Err(), context.Canceled) {
				cancelContext, cancelRequest := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
				defer cancelRequest()
				cancelled, cancelErr := client.CancelAppTask(cancelContext, slug, task.ID)
				if cancelErr != nil {
					PrintWarn(osStderr, "could not request cancellation for Object-storage canary task %s: %v", task.ID, cancelErr)
				} else if !jsonOutput {
					PrintWarn(osStderr, "cancellation requested for Object-storage canary task %s (status=%s)", task.ID, cancelled.Status)
				}
				return report, task, "", 130, nil
			}
			report.Error = "Object-storage canary task did not finish before the wait timeout"
			return report, task, "Object-storage binding canary is still running", 1, waitContext.Err()
		case <-time.After(pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, task.ID)
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				report.Error = "Object-storage canary task did not finish before the wait timeout"
				return report, task, "Object-storage binding canary wait timed out; the task is still running", 1, waitContext.Err()
			}
			report.Error = err.Error()
			return report, task, "Could not read Object-storage binding canary status", 1, err
		}
	}
	if task.StdoutTail != "" {
		var taskReport api.ObjectStorageBindingProbeReport
		if err := json.Unmarshal([]byte(task.StdoutTail), &taskReport); err != nil {
			report.Error = "task did not return a valid Object-storage canary report"
		} else {
			report = taskReport
			if report.Prefix != prefix {
				report.Error = "canary report does not match the selected Object-storage key"
			}
		}
	} else {
		report.Error = "task did not return a Object-storage canary report"
	}
	if report.Prefix == "" {
		report.Prefix = prefix
	}
	report.App = slug
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	if task.OutputTruncated || len(task.StdoutTail) > 4096 {
		report.Error = "canary output was truncated"
	}
	if task.Status != api.AppTaskStatusSucceeded || !report.Passed() || task.OutputTruncated || report.Error != "" || (task.ExitCode == nil || *task.ExitCode != 0) {
		if report.Error == "" {
			report.Error = "Object-storage binding canary did not succeed"
		}
		return report, task, "", 1, nil
	}
	return report, task, "", 0, nil
}

func renderObjectStorageBindingProbeReport(app string, report api.ObjectStorageBindingProbeReport, task api.AppTaskResponse) {
	_, _ = fmt.Fprintf(osStdout, "Object-storage binding canary: %s → %s (read access only)\n", app, report.Prefix)
	_, _ = fmt.Fprintf(osStdout, "Task: %s (deployment=%s)\n", task.ID, task.DeploymentID)
	for _, row := range []struct {
		name  string
		check api.ObjectStorageBindingProbeCheck
	}{
		{"Environment", report.Environment}, {"Configuration", report.Configuration}, {"Connection", report.Connection},
		{"Authorization", report.Authorization}, {"Bucket access", report.BucketAccess},
	} {
		label := strings.ToUpper(strings.ReplaceAll(row.check.Status, "_", " "))
		if label == "" {
			label = "NOT CHECKED"
		}
		if row.check.Detail != "" {
			_, _ = fmt.Fprintf(osStdout, "%-14s %s (%s)\n", row.name, label, row.check.Detail)
		} else {
			_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", row.name, label)
		}
	}
	if report.Error != "" {
		PrintFail(osStderr, "%s", report.Error)
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "canary output was truncated at %d bytes", task.MaxOutputBytes)
	}
}

func newObjectStorageBindingProbeReport(app, prefix string) api.ObjectStorageBindingProbeReport {
	return api.ObjectStorageBindingProbeReport{App: app, Prefix: prefix,
		Environment: api.ObjectStorageBindingProbeCheck{Status: "not_checked"}, Configuration: api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Connection: api.ObjectStorageBindingProbeCheck{Status: "not_checked"}, Authorization: api.ObjectStorageBindingProbeCheck{Status: "not_checked"}, BucketAccess: api.ObjectStorageBindingProbeCheck{Status: "not_checked"}}
}
