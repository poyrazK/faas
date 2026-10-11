package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Status reads never submit, reselect or retry deployment actions.
func cmdAlertActions(args []string) int {
	fs := newFlagSet("alerts actions", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	fire := fs.String("fire", "", "production alert fire UUID (optional)")
	wait := fs.Bool("wait", false, "wait for the selected alert action to complete")
	timeout := fs.Duration("timeout", 10*time.Minute, "wait deadline")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *slug == "" || *wait && *fire == "" || *timeout <= 0 || *interval <= 0 {
		PrintUsage(os.Stderr, alertActionsUsage, "alerts")
		return 1
	}
	if *fire != "" {
		id, err := uuid.Parse(*fire)
		if err != nil {
			return printErr("Invalid fire", fmt.Errorf("--fire must be an alert delivery UUID"))
		}
		*fire = id.String()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *fire != "" {
		if *wait {
			return cmdWaitAlertRollback(client, *slug, *fire, *timeout, *interval)
		}
		receipt, err := client.GetAlertRollback(context.Background(), *slug, *fire)
		if err != nil {
			return printErr("Read alert action failed", err)
		}
		if receipt.ID != *fire {
			return printErr("Invalid alert action receipt", fmt.Errorf("server returned a different alert fire"))
		}
		if jsonOutput {
			return jsonOut(writeJSON(receipt))
		}
		return outputAlertRollback(receipt)
	}
	rows, err := client.ListAlertRollbacks(context.Background(), *slug)
	if err != nil {
		return printErr("List alert actions failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(rows))
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no automatic rollback actions)")
		return 0
	}
	for _, r := range rows {
		if code := outputAlertRollback(r); code != 0 {
			return code
		}
	}
	return 0
}
func outputAlertRollback(r api.AlertRollback) int {
	_, _ = fmt.Fprintf(osStdout, "Alert rollback %s: %s\n  rule: %s\n  candidate: %s\n  predecessor: %s\n", r.ID, r.Status, r.RuleID, r.CandidateDeploymentID, r.PredecessorDeploymentID)
	if r.Code != "" {
		_, _ = fmt.Fprintf(osStdout, "  code: %s\n", r.Code)
	}
	if r.AuditID != "" {
		_, _ = fmt.Fprintf(osStdout, "  audit: %s\n", r.AuditID)
	}
	if r.Historical {
		_, _ = fmt.Fprintf(osStdout, "  historical rollback: %s\n  operation: %s\n  routing audit: %s\n", r.RollbackPhase, r.RollbackOperationID, r.RollbackRoutingAuditID)
	}
	if e := r.DeploymentEvidence; e != nil {
		_, _ = fmt.Fprintf(osStdout, "  deployment evidence: %s\n  observation: %s to %s\n  requests: %d (minimum %d), server errors: %d\n  error rate: %.4g%% (%s %.4g%%)\n",
			e.Status, e.WindowStart.Format(time.RFC3339), e.WindowEnd.Format(time.RFC3339), e.Requests, e.MinimumRequests, e.ServerErrors, e.ErrorRatePct, e.Comparison, e.Threshold)
		if e.Code != "" {
			_, _ = fmt.Fprintf(osStdout, "  evidence reason: %s\n", e.Code)
		}
	}
	if r.Service && !r.Historical {
		_, _ = fmt.Fprintf(osStdout, "  service handoff: %s\n  request: %s\n", r.ServicePhase, r.ServiceRequestID)
		if r.ServiceRoutingAuditID != "" {
			_, _ = fmt.Fprintf(osStdout, "  routing audit: %s\n", r.ServiceRoutingAuditID)
		}
	}
	for _, blocker := range r.Blockers {
		_, _ = fmt.Fprintf(osStdout, "  blocker: %s: %s\n", blocker.Code, blocker.Message)
	}
	return 0
}
