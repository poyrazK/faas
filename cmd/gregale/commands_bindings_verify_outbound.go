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

func runOutboundBindingProbe(ctx context.Context, client bindingInventoryProbeClient, slug, integration string, pollInterval, waitTimeout time.Duration) int {
	inventory, err := client.GetAppBindingInventory(ctx, slug, "")
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	bindings, err := client.ListOutboundAppBindings(ctx, slug)
	if err != nil {
		return printErr("Could not list outbound bindings", err)
	}
	bound := false
	for _, binding := range bindings.Items {
		if binding.Integration.ID == integration {
			bound = true
			break
		}
	}
	if !bound {
		return printErr("Outbound integration is not bound in the selected live scope", fmt.Errorf("%s has no selected managed outbound binding for %s", slug, integration))
	}

	report, task, errorTitle, exitCode, err := executeOutboundBindingProbe(ctx, client, slug, integration, pollInterval, waitTimeout)
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
	renderOutboundBindingProbeReport(slug, report, task)
	return exitCode
}

func executeOutboundBindingProbe(ctx context.Context, client serviceBindingProbeClient, slug, integration string, pollInterval, waitTimeout time.Duration) (api.OutboundBindingProbeReport, api.AppTaskResponse, string, int, error) {
	report := newOutboundBindingProbeReport(slug, integration)
	task, err := client.CreateAppTask(ctx, slug, api.CreateAppTaskRequest{
		Command:        []string{api.AppTaskOutboundBindingProbeCommand, integration},
		TimeoutSeconds: bindingProbeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
	})
	if err != nil {
		report.Error = err.Error()
		return report, task, "Could not start Outbound binding canary", 1, err
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
					PrintWarn(osStderr, "could not request cancellation for Outbound canary task %s: %v", task.ID, cancelErr)
				} else if !jsonOutput {
					PrintWarn(osStderr, "cancellation requested for Outbound canary task %s (status=%s)", task.ID, cancelled.Status)
				}
				return report, task, "", 130, nil
			}
			report.Error = "Outbound canary task did not finish before the wait timeout"
			return report, task, "Outbound binding canary is still running", 1, waitContext.Err()
		case <-time.After(pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, task.ID)
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				report.Error = "Outbound canary task did not finish before the wait timeout"
				return report, task, "Outbound binding canary wait timed out; the task is still running", 1, waitContext.Err()
			}
			report.Error = err.Error()
			return report, task, "Could not read Outbound binding canary status", 1, err
		}
	}
	if task.StdoutTail != "" {
		var taskReport api.OutboundBindingProbeReport
		if err := json.Unmarshal([]byte(task.StdoutTail), &taskReport); err != nil {
			report.Error = "task did not return a valid Outbound canary report"
		} else {
			report = taskReport
			if report.IntegrationID != integration {
				report.Error = "canary report does not match the selected Outbound key"
			}
		}
	} else {
		report.Error = "task did not return a Outbound canary report"
	}
	report.IntegrationID = integration
	report.Configuration.Detail, report.Identity.Detail, report.Gateway.Detail = "", "", ""
	if !safeOutboundStatusDetail(report.Response.Detail) {
		report.Response.Detail = ""
	}
	if report.Error != "" {
		report.Error = "outbound binding canary did not succeed"
	}
	report.App = slug
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	if task.OutputTruncated || len(task.StdoutTail) > 4096 {
		report.Error = "canary output was truncated"
	}
	if task.Status != api.AppTaskStatusSucceeded || !report.Passed() || task.OutputTruncated || report.Error != "" || (task.ExitCode == nil || *task.ExitCode != 0) {
		if report.Error == "" {
			report.Error = "Outbound binding canary did not succeed"
		}
		return report, task, "", 1, nil
	}
	return report, task, "", 0, nil
}

func renderOutboundBindingProbeReport(app string, report api.OutboundBindingProbeReport, task api.AppTaskResponse) {
	_, _ = fmt.Fprintf(osStdout, "Outbound binding canary: %s → %s (configured endpoint)\n", app, report.IntegrationID)
	_, _ = fmt.Fprintf(osStdout, "Task: %s (deployment=%s)\n", task.ID, task.DeploymentID)
	for _, row := range []struct {
		name  string
		check api.OutboundBindingProbeCheck
	}{
		{"Configuration", report.Configuration}, {"Identity", report.Identity}, {"Gateway", report.Gateway}, {"Response", report.Response},
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

func newOutboundBindingProbeReport(app, integration string) api.OutboundBindingProbeReport {
	return api.OutboundBindingProbeReport{App: app, IntegrationID: integration,
		Configuration: api.OutboundBindingProbeCheck{Status: "not_checked"},
		Gateway:       api.OutboundBindingProbeCheck{Status: "not_checked"}, Identity: api.OutboundBindingProbeCheck{Status: "not_checked"}, Response: api.OutboundBindingProbeCheck{Status: "not_checked"}}
}

func safeOutboundStatusDetail(detail string) bool {
	if len(detail) != 8 || !strings.HasPrefix(detail, "http_") {
		return false
	}
	for _, c := range detail[5:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
