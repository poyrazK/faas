package main

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Polling observations do not establish workflow outcomes or webhook delivery.
func printRecoveryPollingHealth(policy api.WorkflowRecoveryPolicyResponse, overview ...*api.WorkflowRecoveryOverview) {
	note := policy.PollingNotification
	status := "no_observed_failure"
	if execution := policy.AutomaticExecution; execution != nil && execution.PollingFailure != nil {
		failure := execution.PollingFailure
		status = "failing"
		_, _ = fmt.Fprintf(osStdout, "Automatic polling: %s | %s | attempts %d | failed at %s | next retry %s\n", status, oneLine(failure.Code), failure.Attempts, failure.FailedAt.Format(time.RFC3339), failure.RetryAt.Format(time.RFC3339))
	} else {
		if note != nil && note.Payload.State == "failed" {
			status = "unknown_pending_successful_poll"
		}
		_, _ = fmt.Fprintf(osStdout, "Automatic polling: %s (monitoring observations only)\n", status)
	}
	if note == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Last polling alert capture: %s | %s | at %s | receivers %d\n", oneLine(note.Payload.State), oneLine(note.Status), note.CapturedAt.Format(time.RFC3339), len(note.ReceiverWebhookIDs))
	if len(overview) > 0 && overview[0] != nil && overview[0].LastAlert != nil && overview[0].LastAlert.Notification.Payload.EventID == note.Payload.EventID {
		_, _ = fmt.Fprintln(osStdout, "Automatic polling alert delivery: "+oneLine(overview[0].LastAlert.DeliveryStatus))
	} else {
		_, _ = fmt.Fprintln(osStdout, "Automatic polling alert delivery: not inspected; capture does not confirm delivery.")
	}
	if note.Payload.InspectionURL != "" {
		_, _ = fmt.Fprintln(osStdout, "Polling inspection: "+oneLine(note.Payload.InspectionURL))
	}
}
