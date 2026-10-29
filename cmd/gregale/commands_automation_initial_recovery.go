package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAutomationsFailures(args []string) int {
	fs := newFlagSet("automations failures", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	workflow := fs.String("name", "", "workflow filter")
	after := fs.String("after", "", "next page cursor")
	limit := fs.Int("limit", api.WorkflowRecoveryHistoryDefaultPage, "page size")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" {
		return printErr("Invalid arguments", errors.New("provide --app"))
	}
	o := api.WorkflowInitialFailureOptions{Workflow: *workflow, After: *after, Limit: *limit}
	if err := o.Validate(); err != nil {
		return printErr("Invalid filters", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.ListWorkflowInitialFailures(ctx, *app, o)
	if err != nil {
		return printErr("Could not inspect workflow failures", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintln(osStdout, "FAILURE\tWORKFLOW\tRUN\tSTATUS\tCAPTURED")
	for _, e := range out.Items {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\n", oneLine(e.ID), oneLine(e.Workflow), oneLine(e.RunID), oneLine(e.FailureStatus), e.ObservedAt.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(osStdout, "Coverage is partial; retained initial failures only. Review with automations failure-recovery-plan --app SLUG --failure UUID.")
	if out.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", oneLine(out.NextAfter))
	}
	return 0
}
func initialAutomationRecoveryPlan(app, appID, id, key string, out api.WorkflowInitialRecoveryPreview) (automationRecoveryPlan, error) {
	p := automationRecoveryPlan{Version: 1, App: app, AppID: appID, Name: out.Failure.Workflow, Key: key, CreatedAt: out.ObservedAt}
	t := out.Target
	if out.Failure.ID != id || out.Failure.AppID != appID || out.ObservedAt.IsZero() || out.Reason != "eligible" || len(out.Blockers) != 0 || t == nil || t.RunID != out.Failure.RunID || t.FailureEvidenceID != id || t.ExpectedResumeCount != 0 || t.ParentPosition != 0 {
		return p, errors.New("initial failure preview is blocked or does not match the selected evidence")
	}
	p.Runs = []automationRecoverySelection{{ID: t.RunID, ExpectedResumeCount: 0, FailureEvidenceID: id}}
	return p, validateAutomationRecoveryPlan(p)
}
func cmdAutomationsFailureRecoveryPlan(args []string) int {
	fs := newFlagSet("automations failure-recovery-plan", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	failure := fs.String("failure", "", "captured initial failure UUID")
	path := fs.String("plan-out", "", "new reviewed recovery plan")
	key := fs.String("key", "", "stable recovery key")
	policyPath := fs.String("policy", "", "candidate policy file for initial-failure dry run")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" || !workflowUUIDPattern.MatchString(*failure) || (*path == "") != (*key == "") {
		return printErr("Invalid arguments", errors.New("provide --app and --failure; saving also requires --key and --plan-out"))
	}
	if *key != "" {
		if err := validateDeployIdempotencyKey(*key); err != nil {
			return printErr("Invalid plan key", err)
		}
	}
	var candidate *api.WorkflowRecoveryPolicy
	if *policyPath != "" {
		req, err := readWorkflowRecoveryPolicyFile(*policyPath)
		if err != nil {
			return printErr("Invalid candidate policy", err)
		}
		candidate = &req.Policy
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	owner, err := c.GetApp(ctx, *app)
	if err != nil {
		return printErr("Could not inspect app", err)
	}
	out, err := c.PreviewWorkflowInitialRecovery(ctx, *app, *failure)
	if err != nil {
		return printErr("Could not review initial failure", err)
	}
	if out.Failure.ID != *failure || out.Failure.AppID != owner.ID {
		return printErr("Invalid preview", errors.New("failure preview scope does not match selected app"))
	}
	policyReason := ""
	if candidate != nil {
		policyReason = api.WorkflowInitialFailurePolicyReason(*candidate, out)
		if *path != "" && policyReason != "eligible" {
			return printErr("Candidate policy excludes recovery", errors.New(policyReason))
		}
	}
	if *path != "" {
		p, err := initialAutomationRecoveryPlan(*app, owner.ID, *failure, *key, out)
		if err != nil {
			return printErr("Cannot prepare initial recovery", fmt.Errorf("%s: %w", out.Reason, err))
		}
		if err = writeAutomationRecoveryPlanFile(*path, p); err != nil {
			return printErr("Could not save recovery plan", err)
		}
	}
	if jsonOutput {
		if candidate != nil {
			return jsonOut(writeJSON(map[string]any{"preview": out, "policy_reason": policyReason, "execution_pressure": "not_evaluated"}))
		}
		return jsonOut(writeJSON(out))
	}
	if candidate != nil {
		_, _ = fmt.Fprintf(osStdout, "Candidate policy: %s (cooldown and budgets are not reserved)\n", oneLine(policyReason))
	}
	_, _ = fmt.Fprintf(osStdout, "Initial failure %s | workflow %s | run %s | %s\n", oneLine(out.Failure.ID), oneLine(out.Failure.Workflow), oneLine(out.Failure.RunID), oneLine(out.Reason))
	for _, code := range out.Blockers {
		_, _ = fmt.Fprintf(osStdout, "  Blocker: %s\n", oneLine(code))
	}
	if *path != "" {
		_, _ = fmt.Fprintf(osStdout, "Saved reviewed plan: %s\nSubmit: gregale automation recovery create --plan %s\n", oneLine(*path), automationCommandArgument(*path))
	}
	return 0
}
