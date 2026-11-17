package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func printRecoveryPolicyChangePreview(ctx context.Context, c *api.Client, app string, req api.SetWorkflowRecoveryPolicyRequest) int {
	out, err := c.PreviewWorkflowRecoveryPolicyChange(ctx, app, req)
	if err != nil {
		return printErr("Could not preview recovery policy change", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Policy change preview: %s | version %d -> %d | observed %s\nProposed: enabled %t | %s\nPending recoveries suppressed: %d | execution slots retained: %d\nAutomatic execution enabled: %t | spent %d/hour | remaining %d\nPreparation: spent %d/hour | remaining %d\n", oneLine(app), out.Current.Version, out.NextVersion, out.ObservedAt.Format(time.RFC3339), out.Proposed.Enabled, oneLine(out.Proposed.Mode), out.PendingSuppressed, out.OutstandingRetained, out.AutomaticExecutionEnabled, out.HourlyExecutions, out.RemainingExecutionBudget, out.HourlyPreparedActions, out.RemainingPreparationBudget)
	if a := out.Current.AutomaticExecution; a != nil && (a.PausedAt != nil || a.ConsecutiveFailures > 0) {
		_, _ = fmt.Fprintln(osStdout, "Replacement clears the current automatic pause and failure streak.")
	}
	if out.ExecutionCooldownUntil != nil {
		_, _ = fmt.Fprintf(osStdout, "Execution cooldown until: %s\n", out.ExecutionCooldownUntil.Format(time.RFC3339))
	}
	if out.PreparationCooldownUntil != nil {
		_, _ = fmt.Fprintf(osStdout, "Preparation cooldown until: %s\n", out.PreparationCooldownUntil.Format(time.RFC3339))
	}
	for _, e := range out.Entries {
		_, _ = fmt.Fprintf(osStdout, "  %s | review %s | action %s | child %s\n", oneLine(e.Effect), oneLine(e.Entry.ReviewID), oneLine(e.Entry.Action.ActionID), oneLine(e.Entry.Action.ChildJobID))
	}
	for _, c := range out.Candidates {
		_, _ = fmt.Fprintf(osStdout, "  Job %s | run %s | generation %d | %s | %s | %s\n", oneLine(c.JobID), oneLine(c.Decision.RunID), c.Decision.ResumeCount, oneLine(c.Decision.Action), oneLine(c.Decision.Trigger), oneLine(c.Decision.Outcome))
	}
	if initial := out.InitialFailures; initial != nil {
		_, _ = fmt.Fprintf(osStdout, "Initial failures: %d inspected | eligible %d | excluded %d | safety blocked %d | would prepare %d | would submit %d\n", initial.InspectedFailures, initial.Eligible, initial.Excluded, initial.SafetyBlocked, initial.WouldPrepare, initial.WouldSubmit)
		_, _ = fmt.Fprintf(osStdout, "Initial pressure: %d retained reviews | review capacity %d | %d active account recoveries | job capacity %d | unresolved app recovery %t\n", initial.RetainedReviews, initial.RemainingReviewCapacity, initial.ActiveRecoveryJobs, initial.RemainingRecoveryJobCapacity, initial.UnresolvedRecovery)
		if initial.RuntimeAvailable != nil {
			_, _ = fmt.Fprintf(osStdout, "Workflow runtime available: %t\n", *initial.RuntimeAvailable)
		}
		for _, c := range initial.Candidates {
			_, _ = fmt.Fprintf(osStdout, "  Failure %s | workflow %s | run %s | %s | eligibility %s | preparation %s | execution %s\n", oneLine(c.Failure.ID), oneLine(c.Failure.Workflow), oneLine(c.Failure.RunID), oneLine(c.Failure.FailureStatus), oneLine(c.Eligibility), oneLine(c.Preparation), oneLine(c.Execution))
			for _, blocker := range c.Blockers {
				_, _ = fmt.Fprintf(osStdout, "    Blocker: %s\n", oneLine(blocker))
			}
		}
		_, _ = fmt.Fprintf(osStdout, "Initial sample: %s coverage | truncated %t | matching filters first, then failure ID\n", oneLine(initial.Coverage), initial.Truncated)
	}
	_, _ = fmt.Fprintf(osStdout, "Inspected jobs: %d | jobs truncated %t | candidates truncated %t | queue entries truncated %t\n", out.InspectedJobs, out.JobsTruncated, out.CandidatesTruncated, out.EntriesTruncated)
	_, _ = fmt.Fprintln(osStdout, "Advisory only. Applying the file requires the observed version and write access; execution rechecks current evidence, budgets and quota.")
	return 0
}
