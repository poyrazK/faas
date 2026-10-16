package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strings"
	"time"
)

func readWorkflowRecoveryPolicyFile(path string) (api.SetWorkflowRecoveryPolicyRequest, error) {
	var req api.SetWorkflowRecoveryPolicyRequest
	f, err := openCustomerFile(path)
	if err != nil {
		return req, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return req, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.EventRecoveryRequestMaxBytes {
		return req, errors.New("policy must be a regular file of at most 64 KiB")
	}
	d := json.NewDecoder(io.LimitReader(f, api.EventRecoveryRequestMaxBytes+1))
	d.DisallowUnknownFields()
	if err = d.Decode(&req); err != nil {
		return req, err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return req, errors.New("expected one policy JSON object")
	}
	return req, req.Validate()
}
func cmdAutomationsRecoveryPolicy(args []string) int {
	fs := newFlagSet("automations recovery-policy", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	path := fs.String("file", "", "policy update JSON with expected_version")
	control := fs.String("control", "", "pause or resume automatic execution")
	reason := fs.String("reason", "", "operator control reason")
	preview := fs.Bool("preview", false, "preview a proposed --file policy without changing it")
	queue := fs.Bool("queue", false, "inspect automatic retry queue and remaining budget")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" {
		return printErr("Invalid policy scope", errors.New("provide --app"))
	}
	if (*preview && (*path == "" || *control != "" || *queue)) || (*control != "" && (*path != "" || *queue)) || (*queue && *path != "") || (*reason != "" && *control == "") || (*control != "" && *control != "pause" && *control != "resume") || (*control != "" && strings.TrimSpace(*reason) == "") {
		return printErr("Invalid policy operation", errors.New("use one of --file, --queue, or --control pause|resume --reason"))
	}
	var req api.SetWorkflowRecoveryPolicyRequest
	var err error
	if *path != "" {
		req, err = readWorkflowRecoveryPolicyFile(*path)
		if err != nil {
			return printErr("Invalid recovery policy", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	if *queue {
		out, e := c.GetWorkflowRecoveryAutomaticQueue(ctx, *app)
		if e != nil {
			return printErr("Could not inspect automatic queue", e)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Automatic queue: %s | policy %d | %s | pending %d | outstanding %d | budget remaining %d/%d\n", oneLine(*app), out.Policy.Version, oneLine(out.Policy.Status), out.Pending, out.Outstanding, out.RemainingBudget, out.Policy.Policy.HourlyExecutionBudget)
		if out.CooldownUntil != nil {
			_, _ = fmt.Fprintf(osStdout, "Cooldown until: %s\n", out.CooldownUntil.Format(time.RFC3339))
		}
		for _, e := range out.Entries {
			_, _ = fmt.Fprintf(osStdout, "  Review %s | policy %d | action %s | %s | %s | child %s\n", oneLine(e.ReviewID), e.PolicyVersion, oneLine(e.Action.ActionID), oneLine(e.Action.Status), oneLine(e.Action.Reason), oneLine(e.Action.ChildJobID))
			_, _ = fmt.Fprintf(osStdout, "    Queued: %s | age %ds\n", e.QueuedAt.Format(time.RFC3339), e.QueuedAgeSeconds)
			if e.WaitingAgeSeconds != nil {
				_, _ = fmt.Fprintf(osStdout, "    Current observed wait: %ds\n", *e.WaitingAgeSeconds)
			}
			if e.Action.LastEvaluation == nil {
				_, _ = fmt.Fprintln(osStdout, "    Last evaluation: not recorded")
			}
			printAutomaticActionEvidence(e.Action)
			printRecoveryQueueEvidence(e)
		}
		if out.Truncated {
			_, _ = fmt.Fprintln(osStdout, "Queue entries truncated; use recovery-policy-reviews to inspect retained reviews.")
		}
		return 0
	}
	if *preview {
		return printRecoveryPolicyChangePreview(ctx, c, *app, req)
	}
	var out api.WorkflowRecoveryPolicyResponse
	if *control != "" {
		current, e := c.GetWorkflowRecoveryPolicy(ctx, *app)
		if e != nil {
			return printErr("Could not inspect policy before control", e)
		}
		request := api.ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: current.Version, ExpectedUpdatedAt: current.UpdatedAt, Operation: *control, Reason: *reason}
		if current.AutomaticExecution != nil {
			request.ExpectedPausedAt = current.AutomaticExecution.PausedAt
		}
		out, err = c.ControlWorkflowRecoveryPolicy(ctx, *app, request)
	} else if *path == "" {
		out, err = c.GetWorkflowRecoveryPolicy(ctx, *app)
	} else {
		out, err = c.SetWorkflowRecoveryPolicy(ctx, *app, req)
	}
	if err != nil {
		return printErr("Recovery policy request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery policy: %s | version %d | %s | enabled %t | %s\nWorkflows: %s\nTriggers: %s\nMax resume count: %d | cooldown: %ds | hourly preparation budget: %d\n", oneLine(*app), out.Version, oneLine(out.Policy.Mode), out.Policy.Enabled, oneLine(out.Status), oneLine(strings.Join(out.Policy.WorkflowNames, ", ")), oneLine(strings.Join(out.Policy.Triggers, ", ")), out.Policy.MaxResumeCount, out.Policy.CooldownSeconds, out.Policy.HourlyActionBudget)
	if n := out.PollingNotification; n != nil {
		_, _ = fmt.Fprintf(osStdout, "Polling alert: %s | %s | event %s\n", oneLine(n.Payload.State), oneLine(n.Payload.Code), oneLine(n.Payload.EventID))
	}
	if a := out.AutomaticExecution; a != nil {
		if f := a.PollingFailure; f != nil {
			_, _ = fmt.Fprintf(osStdout, "Polling error: %s | attempts %d | next retry %s\n", oneLine(f.Code), f.Attempts, f.RetryAt.UTC().Format(time.RFC3339))
		}
		_, _ = fmt.Fprintf(osStdout, "Automatic execution budget: %d/hour | pause after %d consecutive failures | failures %d\n", out.Policy.HourlyExecutionBudget, out.Policy.PauseAfterFailures, a.ConsecutiveFailures)
		if a.PausedAt != nil {
			_, _ = fmt.Fprintf(osStdout, "Paused: %s | %s\n", a.PausedAt.Format(time.RFC3339), oneLine(a.PauseReason))
		}
	}
	return 0
}
func cmdAutomationsRecoveryPolicyReviews(args []string) int {
	fs := newFlagSet("automations recovery-policy-reviews", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	id := fs.String("review", "", "retained review ID")
	path := fs.String("plan-out", "", "export the selected review plan")
	decision := fs.String("decision", "", "approved or dismissed; records review only")
	reason := fs.String("reason", "", "review decision reason")
	status := fs.Bool("status", false, "inspect live plan freshness and applied receipts")
	after := fs.String("after", "", "review pagination cursor")
	limit := fs.Int("limit", api.EventRecoveryHistoryPageMax, "review page limit")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || *id != "" && !workflowUUIDPattern.MatchString(*id) || *path != "" && *id == "" || *limit < 1 || *limit > api.EventRecoveryHistoryPageMax || *after != "" && !workflowUUIDPattern.MatchString(*after) || *id != "" && *after != "" {
		return printErr("Invalid policy review arguments", errors.New("provide --app; exporting requires --review and --plan-out"))
	}
	if (*decision != "" || *status) && *id == "" || *decision != "" && (*status || *path != "") || *status && *path != "" || *reason != "" && *decision == "" {
		return printErr("Invalid review operation", errors.New("decision or status requires --review; use one operation at a time"))
	}
	req := api.WorkflowRecoveryReviewDecisionRequest{Decision: *decision, Reason: *reason}
	if *decision != "" {
		if e := req.Validate(); e != nil {
			return printErr("Invalid review decision", e)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	if *status {
		out, e := c.GetWorkflowRecoveryReviewStatus(ctx, *app, *id)
		if e != nil {
			return printErr("Could not inspect review status", e)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Review %s | %s | outcome %s | complete %t\n", oneLine(out.ReviewID), oneLine(out.Status), oneLine(out.Outcome), out.Complete)
		for _, a := range out.Actions {
			_, _ = fmt.Fprintf(osStdout, "  Item %d | %s | %s | %s | outcome %s | complete %t | run %s generation %d\n", a.Position, oneLine(a.Kind), oneLine(a.Status), oneLine(a.Reason), oneLine(a.Outcome), a.Complete, oneLine(a.RunID), a.ResumeCount)
			if a.ChildJobID != "" {
				_, _ = fmt.Fprintf(osStdout, "    Child %s | items %s\n", oneLine(a.ChildJobID), oneLine(a.ItemsURL))
			}
			if a.EvidenceSource != "" {
				_, _ = fmt.Fprintf(osStdout, "    Evidence %s | run %s\n", oneLine(a.EvidenceSource), oneLine(a.RunURL))
			}
			if a.RecordedAt != nil {
				_, _ = fmt.Fprintf(osStdout, "    Recorded %s\n", a.RecordedAt.Format(time.RFC3339))
			}
			if a.OutcomeReason != "" {
				_, _ = fmt.Fprintf(osStdout, "    Outcome reason %s\n", oneLine(a.OutcomeReason))
			}
		}
		return 0
	}
	out := api.WorkflowRecoveryPolicyReviews{Reviews: []api.WorkflowRecoveryPolicyReview{}}
	if *id != "" {
		var review api.WorkflowRecoveryPolicyReview
		var e error
		if *decision != "" {
			review, e = c.DecideWorkflowRecoveryPolicyReview(ctx, *app, *id, req)
		} else {
			review, e = c.GetWorkflowRecoveryPolicyReview(ctx, *app, *id)
		}
		err = e
		if err == nil {
			if review.ID != *id {
				return printErr("Review identity changed", errors.New("review ID does not match"))
			}
			out.Reviews = append(out.Reviews, review)
		}
	} else {
		out, err = c.ListWorkflowRecoveryPolicyReviews(ctx, *app, *after, *limit)
	}
	if err != nil {
		return printErr("Could not inspect recovery policy reviews", err)
	}
	if *path != "" {
		review := out.Reviews[0]
		if review.Plan == nil {
			return printErr("Review has no prepared plan", errors.New("inspect suppression decisions"))
		}
		owner, e := c.GetApp(ctx, *app)
		if e != nil {
			return printErr("Could not inspect review scope", e)
		}
		if review.AppID != owner.ID || review.Plan.AppID != owner.ID || review.Plan.App != *app || review.Plan.JobID != review.JobID {
			return printErr("Review scope changed", errors.New("prepared plan must match the selected app and recovery job"))
		}
		if err = saveAutomationResponsePlan(*path, *review.Plan); err != nil {
			return printErr("Could not export prepared plan", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	for _, review := range out.Reviews {
		_, _ = fmt.Fprintf(osStdout, "Review %s | job %s | policy version %d | prepared %t\n", oneLine(review.ID), oneLine(review.JobID), review.PolicyVersion, review.Plan != nil)
		if execution := review.AutomaticExecution; execution != nil {
			for _, a := range execution.Actions {
				_, _ = fmt.Fprintf(osStdout, "  Automatic item %d: %s | %s | action %s | child %s\n", a.Position, oneLine(a.Status), oneLine(a.Reason), oneLine(a.ActionID), oneLine(a.ChildJobID))
				printAutomaticActionEvidence(a)
			}
		}
		if n := review.CompletionNotification; n != nil {
			_, _ = fmt.Fprintf(osStdout, "  Completion: %s | outcome %s | receivers %d | event %s\n", oneLine(n.Status), oneLine(n.Result.Outcome), len(n.ReceiverWebhookIDs), oneLine(n.EventID))
		}
		if n := review.Notification; n != nil {
			_, _ = fmt.Fprintf(osStdout, "  Notification: %s | %s | receivers %d | event %s\n", oneLine(n.Status), oneLine(n.Reason), len(n.ReceiverWebhookIDs), oneLine(n.EventID))
		}
		if review.ReviewDecision != nil {
			d := review.ReviewDecision
			_, _ = fmt.Fprintf(osStdout, "  Decision: %s | %s %s | %s\n", oneLine(d.Decision), oneLine(d.ActorKind), oneLine(d.ActorID), oneLine(d.Reason))
		}
		for _, d := range review.Decisions {
			_, _ = fmt.Fprintf(osStdout, "  Item %d | run %s | generation %d | %s | %s | %s\n", d.Position, oneLine(d.RunID), d.ResumeCount, oneLine(d.Trigger), oneLine(d.Action), oneLine(d.Outcome))
		}
	}
	if *path != "" {
		_, _ = fmt.Fprintf(osStdout, "Saved prepared plan: %s\nPreview: gregale automations recovery-response-preview --plan %s\n", oneLine(*path), automationCommandArgument(*path))
	}
	if out.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next --after: %s\n", oneLine(out.NextAfter))
	}
	return 0
}

func printAutomaticActionEvidence(a api.WorkflowRecoveryAutomaticAction) {
	if e := a.LastEvaluation; e != nil {
		_, _ = fmt.Fprintf(osStdout, "    Last evaluation: %s | %s\n", e.EvaluatedAt.Format(time.RFC3339), oneLine(e.Reason))
		if e.WaitingSince != nil {
			_, _ = fmt.Fprintf(osStdout, "    Waiting since: %s\n", e.WaitingSince.Format(time.RFC3339))
		}
	}
	if a.Suppression != nil {
		r := a.Suppression
		_, _ = fmt.Fprintf(osStdout, "    Suppression: %s %s | %s | %s\n", oneLine(r.ActorKind), oneLine(r.ActorID), oneLine(r.Reason), r.SuppressedAt.Format(time.RFC3339))
	}
	if a.BlockedNotification != nil {
		r := a.BlockedNotification
		_, _ = fmt.Fprintf(osStdout, "    Queue notification: %s | %s | event %s | %s\n", oneLine(r.Status), oneLine(r.Reason), oneLine(r.EventID), r.CapturedAt.Format(time.RFC3339))
	}
}

func printRecoveryQueueEvidence(e api.WorkflowRecoveryAutomaticQueueEntry) {
	if e.WaitCategory != "" {
		_, _ = fmt.Fprintf(osStdout, "    Wait: %s | %s (current policy or last worker observation)\n", oneLine(e.WaitCategory), oneLine(e.WaitReason))
	}
	if e.EvidenceUnavailableReason != "" {
		_, _ = fmt.Fprintf(osStdout, "    Current evidence unavailable: %s\n", oneLine(e.EvidenceUnavailableReason))
	}
	if e.InitialEvidence != nil {
		d := e.InitialEvidence.Diagnosis
		_, _ = fmt.Fprintf(osStdout, "    Initial evidence: %s | run %s | target generation %d | %s | repairable %t | observed %s\n", oneLine(e.InitialEvidence.Stage), oneLine(d.RunID), d.ResumeCount, oneLine(d.Reason), d.Repairable, d.ObservedAt.Format(time.RFC3339))
		if e.InitialEvidence.Stage == "prepared" {
			_, _ = fmt.Fprintln(osStdout, "    Prepared review: no recovery job submitted; evidence repair unavailable.")
		}
		_, _ = fmt.Fprintf(osStdout, "    Inspect: %s\n    Diagnosis: %s\n", oneLine(e.InitialEvidence.CLICommand), oneLine(e.InitialEvidence.EvidenceURL))
	}
	if e.Evidence == nil {
		return
	}
	d := e.Evidence.Diagnosis
	_, _ = fmt.Fprintf(osStdout, "    Current evidence: %s | run %s | generation %d | %s | repairable %t | observed %s\n", oneLine(d.EvidenceKind), oneLine(d.RunID), d.ResumeCount, oneLine(d.Reason), d.Repairable, d.ObservedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintf(osStdout, "    Inspect: %s\n    Diagnosis: %s\n", oneLine(e.Evidence.CLICommand), oneLine(e.Evidence.EvidenceURL))
}
