package main

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func printRecoveryOverview(o *api.WorkflowRecoveryOverview, app string) {
	if o == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery overview: %s | monitoring %s | incomplete %t\nManual initial reviews awaiting approval: %d | unresolved: %d | polling failures: %d | attention: %d\n", oneLine(o.Status), oneLine(o.MonitoringStatus), o.Truncated, o.ManualAwaitingApproval, o.ManualUnresolved, o.ManualPollingFailures, o.ManualAttention)
	if a := o.LastAlert; a != nil {
		_, _ = fmt.Fprintf(osStdout, "Last retained polling alert delivery: %s | scope %s | expected %d | pending %d | in flight %d | succeeded %d | failed %d | dead %d | missing %d\n", oneLine(a.DeliveryStatus), oneLine(a.Notification.Payload.Scope), a.Expected, a.Pending, a.InFlight, a.Succeeded, a.Failed, a.Dead, a.Missing)
		_, _ = fmt.Fprintf(osStdout, "Polling alert: %s | %s\nInspect: %s\n", oneLine(a.Notification.Payload.EventID), oneLine(a.Notification.Payload.State), oneLine(a.Notification.Payload.InspectionURL))
	}
	_, _ = fmt.Fprintf(osStdout, "Manual retry reviews awaiting inspection: %d | approved awaiting completion observation: %d\n", o.ManualRetryAwaitingReview, o.ManualRetryUnresolved)
	for _, i := range o.Issues {
		_, _ = fmt.Fprintf(osStdout, "Issue: %s | %s | policy version %d | review %s | job %s\nInspect: %s\n", oneLine(i.Scope), oneLine(i.Reason), i.PolicyVersion, oneLine(i.ReviewID), oneLine(i.JobID), oneLine(i.InspectionURL))
		if i.RetryAt != nil {
			_, _ = fmt.Fprintln(osStdout, "Next polling retry: "+i.RetryAt.Format(time.RFC3339))
		}
		if i.Scope == "manual_initial" && i.ReviewID != "" {
			_, _ = fmt.Fprintf(osStdout, "Inspect command: gregale automation failure-recovery review --app %s --review %s\n", automationCommandArgument(app), oneLine(i.ReviewID))
		}
		if i.Scope == "manual_retry" && i.ReviewID != "" {
			_, _ = fmt.Fprintf(osStdout, "Inspect command: gregale automation recovery-policy-reviews --app %s --review %s --status\n", automationCommandArgument(app), oneLine(i.ReviewID))
		}
	}
}
