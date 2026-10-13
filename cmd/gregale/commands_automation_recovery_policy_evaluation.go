package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAutomationsRecoveryPolicyEvaluate(args []string) int {
	fs := newFlagSet("automations recovery-policy-evaluate", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	path := fs.String("file", "", "proposed policy JSON with expected_version")
	workflow := fs.String("workflow", "", "restrict historical review sample to this workflow")
	since := fs.String("since", "7d", "lookback duration, maximum 30 days")
	start := fs.String("created-after", "", "inclusive RFC3339 start; cannot combine with --since")
	end := fs.String("created-before", "", "inclusive RFC3339 end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || *path == "" {
		return printErr("Invalid historical evaluation scope", errors.New("provide --app and --file"))
	}
	policy, err := readWorkflowRecoveryPolicyFile(*path)
	if err != nil {
		return printErr("Invalid proposed policy", err)
	}
	req := api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: policy.ExpectedVersion, Policy: policy.Policy, Workflow: *workflow}
	if *end != "" {
		req.CreatedBefore, err = time.Parse(time.RFC3339Nano, *end)
		if err != nil {
			return printErr("Invalid evaluation end", err)
		}
	}
	explicitSince := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "since" {
			explicitSince = true
		}
	})
	if *start != "" {
		if explicitSince {
			return printErr("Invalid evaluation window", errors.New("choose --since or --created-after"))
		}
		req.CreatedAfter, err = time.Parse(time.RFC3339Nano, *start)
		if err != nil {
			return printErr("Invalid evaluation start", err)
		}
	} else {
		d, err := recoveryReportDuration(*since)
		if err != nil {
			return printErr("Invalid evaluation lookback", err)
		}
		at := req.CreatedBefore
		if at.IsZero() {
			at = time.Now().UTC()
			req.CreatedBefore = at
		}
		req.CreatedAfter = at.Add(-d)
	}
	if err = req.Validate(); err != nil {
		return printErr("Invalid evaluation request", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.EvaluateWorkflowRecoveryPolicy(ctx, *app, req)
	if err != nil {
		return printErr("Could not evaluate historical recovery policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Historical policy evaluation | current version %d | %s to %s\n", out.Current.Version, out.CreatedAfter.Format(time.RFC3339), out.CreatedBefore.Format(time.RFC3339))
	_, _ = fmt.Fprintf(osStdout, "Failure coverage: %s | capture started %s | %d captured, %d reviewed, %d unreviewed, %d missing eligibility checks | truncated %t\n", oneLine(out.FailureCoverage.Coverage), out.FailureCoverage.CaptureStartedAt.Format(time.RFC3339), out.FailureCoverage.CapturedFailures, out.FailureCoverage.ReviewedFailures, out.FailureCoverage.UnreviewedFailures, out.FailureCoverage.MissingEligibilityEvidence, out.FailureCoverage.Truncated)
	printRecoveryPolicyEvaluationTotals("Current settings", out.CurrentTotals)
	printRecoveryPolicyEvaluationTotals("Proposed settings", out.ProposedTotals)
	for _, e := range out.Candidates {
		_, _ = fmt.Fprintf(osStdout, "%s | run %s | generation %d | recorded %s\n", oneLine(e.Workflow), oneLine(e.RunID), e.ResumeCount, oneLine(e.RecordedOutcome))
		for _, a := range []struct {
			label string
			value api.WorkflowRecoveryPolicyAssessment
		}{{"current", e.Current}, {"proposed", e.Proposed}} {
			_, _ = fmt.Fprintf(osStdout, "  %s: %s | %s | preparation %s | execution %s\n", a.label, oneLine(a.value.Eligibility), oneLine(a.value.Reason), oneLine(a.value.Preparation), oneLine(a.value.ExecutionPressure))
		}
		if e.ReviewID != "" {
			_, _ = fmt.Fprintf(osStdout, "  Evidence: /v1/apps/%s/recovery-policy/reviews/%s | observed applications/hour %d\n", url.PathEscape(*app), url.PathEscape(e.ReviewID), e.ObservedHourlyExecutions)
		}
		if e.FailureEvidenceID != "" {
			_, _ = fmt.Fprintf(osStdout, "  Failure evidence: %s | failure-time retry checks not captured\n", oneLine(e.FailureEvidenceID))
		}
		if later := e.LaterEligibility; later != nil {
			_, _ = fmt.Fprintf(osStdout, "  Later retry checks: %s | %s | %s | proposed %s (%s) | blockers %s\n", later.Observation.ObservedAt.Format(time.RFC3339), oneLine(later.Freshness), oneLine(later.Observation.Reason), oneLine(later.Proposed.Eligibility), oneLine(later.Proposed.Reason), oneLine(strings.Join(later.Observation.Blockers, ", ")))
		}
		if e.CandidateEvidenceID != "" {
			_, _ = fmt.Fprintf(osStdout, "  Candidate evidence: %s\n", e.CandidateEvidenceID)
		}
		for _, id := range e.ExecutionEvidenceIDs {
			_, _ = fmt.Fprintf(osStdout, "  Application evidence: %s\n", oneLine(id))
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Sample: %d reviews, %d candidates, %d recorded applications | truncated reviews %t, candidates %t, applications %t\n", out.InspectedReviews, len(out.Candidates), out.ObservedExecutions, out.ReviewsTruncated, out.CandidatesTruncated, out.ExecutionsTruncated)
	for _, note := range out.CoverageNotes {
		_, _ = fmt.Fprintln(osStdout, oneLine(note))
	}
	return 0
}
func printRecoveryPolicyEvaluationTotals(label string, t api.WorkflowRecoveryPolicyEvaluationTotals) {
	_, _ = fmt.Fprintf(osStdout, "%s | conditional preparations %d | excluded %d | unknown %d | cooldown %d | preparation budget %d | deduplicated %d | observed execution budget pressure %d | observed execution cooldown pressure %d\n", label, t.WouldPrepare, t.Excluded, t.Unknown, t.Cooldown, t.PreparationBudget, t.Deduplicated, t.ExecutionBudgetPressure, t.ExecutionCooldownPressure)
}
