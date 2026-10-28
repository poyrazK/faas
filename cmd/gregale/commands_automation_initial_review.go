package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func cmdAutomationsFailureRecoveryReview(args []string) int {
	fs := newFlagSet("automations failure-recovery-review", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	failure := fs.String("failure", "", "captured failure UUID to prepare")
	version := fs.Int64("expected-policy-version", 0, "saved policy version for preparation")
	review := fs.String("review", "", "durable review UUID")
	decision := fs.String("decision", "", "submit or cancel a prepared review")
	reason := fs.String("reason", "", "operator decision reason")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" || (*failure == "") == (*review == "") || *failure != "" && (!workflowUUIDPattern.MatchString(*failure) || *version < 1 || *decision != "" || *reason != "") || *review != "" && (!workflowUUIDPattern.MatchString(*review) || *version != 0) || *decision == "" && *reason != "" {
		return printErr("Invalid arguments", errors.New("provide --app and either --failure with --expected-policy-version, or --review; decisions require --decision and --reason"))
	}
	req := api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: *decision, Reason: *reason}
	if *decision != "" {
		if err := req.Validate(); err != nil {
			return printErr("Invalid decision", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.WorkflowInitialRecoveryReview
	if *failure != "" {
		out, err = c.PrepareWorkflowInitialRecoveryReview(ctx, *app, *failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: *version})
	} else if *decision != "" {
		out, err = c.DecideWorkflowInitialRecoveryReview(ctx, *app, *review, req)
	} else {
		out, err = c.GetWorkflowInitialRecoveryReview(ctx, *app, *review)
	}
	if err != nil {
		return printErr("Could not review initial recovery", err)
	}
	exit := 0
	if *decision == "submit" && out.Status != "submitted" {
		exit = 1
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(out)); code != 0 {
			return code
		}
		return exit
	}
	_, _ = fmt.Fprintf(osStdout, "Review %s | %s | %s | policy %d\n", oneLine(out.ID), oneLine(out.Status), oneLine(out.Reason), out.PolicyVersion)
	if out.JobID != "" {
		_, _ = fmt.Fprintf(osStdout, "Recovery job: %s\n", oneLine(out.JobID))
	}
	if f := out.ObservationFailure; f != nil {
		_, _ = fmt.Fprintf(osStdout, "Observation error: %s | attempts %d | next retry %s\n", oneLine(f.Code), f.Attempts, f.RetryAt.UTC().Format(time.RFC3339))
	}
	if n := out.PollingNotification; n != nil {
		_, _ = fmt.Fprintf(osStdout, "Polling alert: %s | %s | event %s\n", oneLine(n.Payload.State), oneLine(n.Payload.Code), oneLine(n.Payload.EventID))
	}
	if out.Attention != nil {
		a := out.Attention
		label := a.BlockReason
		if label == "" {
			label = "none"
		}
		_, _ = fmt.Fprintf(osStdout, "Observed recovery: %s | attention %s | checked %s\n", oneLine(a.Reason), oneLine(label), a.ObservedAt.UTC().Format(time.RFC3339))
		if a.Diagnosis != nil {
			_, _ = fmt.Fprintf(osStdout, "Evidence: %s | repairable %t\n", oneLine(a.Diagnosis.Reason), a.Diagnosis.Repairable)
		}
		if a.BlockReason != "" {
			_, _ = fmt.Fprintf(osStdout, "%s\nInspect: gregale automation failure-recovery-evidence --app %s --review %s\n", oneLine(a.OperatorAction), automationCommandArgument(*app), oneLine(out.ID))
		}
	}
	if out.Result != nil {
		_, _ = fmt.Fprintf(osStdout, "Result: %s | generation %d\n", oneLine(out.Result.Outcome), out.Result.ResumeCount)
	}
	if out.Status == "prepared" {
		_, _ = fmt.Fprintf(osStdout, "Submit: gregale automation failure-recovery-review --app %s --review %s --decision submit --reason REASON\n", automationCommandArgument(*app), oneLine(out.ID))
	}
	return exit
}
func cmdAutomationsFailureRecoveryReviews(args []string) int {
	fs := newFlagSet("automations failure-recovery-reviews", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	after := fs.String("after", "", "next review cursor")
	limit := fs.Int("limit", api.EventRecoveryHistoryPageMax, "page size")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" || *limit < 1 || *limit > api.EventRecoveryHistoryPageMax || *after != "" && !workflowUUIDPattern.MatchString(*after) {
		return printErr("Invalid arguments", errors.New("provide --app and a valid page size or cursor"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.ListWorkflowInitialRecoveryReviews(ctx, *app, *after, *limit)
	if err != nil {
		return printErr("Could not list initial recovery reviews", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	for _, r := range out.Reviews {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", oneLine(r.ID), oneLine(r.Failure.Workflow), oneLine(r.Status), oneLine(r.Reason))
	}
	if out.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", oneLine(out.NextAfter))
	}
	return 0
}
