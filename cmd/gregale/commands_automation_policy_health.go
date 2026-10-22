package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func printAutomationRecoveryHealth(app string, seconds int) int {
	if err := api.ValidateRecoveryHealthThreshold(seconds); err != nil {
		return printErr("Invalid health threshold", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.GetWorkflowRecoveryPolicyHealth(ctx, app, seconds)
	if err != nil {
		return printErr("Could not inspect recovery automation health", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery automation health: %s | %s\nPending: %d | outstanding: %d | remaining execution budget: %d\nWorker evaluation threshold: %ds | queue sample truncated: %t\n", oneLine(out.Status), oneLine(out.Reason), out.Queue.Pending, out.Queue.Outstanding, out.Queue.RemainingBudget, out.StallAfterSeconds, out.Queue.Truncated)
	printRecoveryPollingHealth(out.Queue.Policy, out.Overview)
	printRecoveryOverview(out.Overview, app)
	if settings := out.Queue.Policy.Policy.HealthAlerts; settings != nil {
		_, _ = fmt.Fprintf(osStdout, "Health alerts: enabled %t | monitor stall threshold %ds | alert after %ds\n", settings.Enabled, settings.StallAfterSeconds, settings.AlertAfterSeconds)
	}
	if incident := out.Queue.HealthIncident; incident != nil {
		alert, recovery := "not_captured", "not_captured"
		if incident.AlertNotification != nil {
			alert = incident.AlertNotification.Status
		}
		if incident.RecoveryNotification != nil {
			recovery = incident.RecoveryNotification.Status
		}
		_, _ = fmt.Fprintf(osStdout, "Health incident: %s | %s | closed %t | alert %s | recovery %s\n", oneLine(incident.EpisodeID), oneLine(incident.Status), incident.ClosedAt != nil, oneLine(alert), oneLine(recovery))
	}
	if out.SampleLastProgressAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Latest progress in retained queue sample: %s\n", out.SampleLastProgressAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	_, _ = fmt.Fprintln(osStdout, "Use automations recovery-policy --app "+oneLine(app)+" --queue to inspect individual retry observations.")
	return 0
}
