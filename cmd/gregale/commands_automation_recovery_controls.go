package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strings"
)

func cmdAutomationsRecoveryHealth(args []string) int {
	fs := newFlagSet("automations recovery-health", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" {
		return printErr("Invalid recovery health arguments", errors.New("provide --app"))
	}
	return cmdEventsRecoveryHealth([]string{*app})
}
func cmdAutomationsRecoveryCancellation(args []string, preview bool) int {
	fs := newFlagSet("automations recovery-cancel", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	job := fs.String("job", "", "recovery job ID")
	position := fs.Int64("position", 0, "reviewed item position")
	count := fs.Int("resume-count", -1, "reviewed admitted generation")
	reason := fs.String("reason", "", "operator reason")
	yes := fs.Bool("yes", false, "confirm cancellation of the reviewed generation")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	req := api.WorkflowRecoveryCancellationRequest{Position: *position, ExpectedResumeCount: count, Reason: *reason}
	if strings.TrimSpace(*app) == "" || !workflowUUIDPattern.MatchString(*job) {
		return printErr("Invalid recovery cancellation arguments", errors.New("provide --app and --job"))
	}
	if err := req.Validate(); err != nil {
		return printErr("Invalid recovery cancellation arguments", err)
	}
	if !preview && !*yes {
		return printErr("Cancellation requires confirmation", errors.New("review recovery-cancel-preview, then provide --yes with the same position and resume count"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	a, err := c.GetApp(ctx, *app)
	if err != nil {
		return printErr("Could not inspect app", err)
	}
	j, err := c.GetEventRecovery(ctx, *job)
	if err != nil {
		return printErr("Could not inspect recovery job", err)
	}
	if j.AppID != a.ID || j.Selection.WorkflowRuns == nil {
		return printErr("Recovery scope changed", errors.New("job must be a native workflow recovery in the selected app"))
	}
	var out api.WorkflowRecoveryCancellationResponse
	if preview {
		out, err = c.PreviewWorkflowRecoveryCancellation(ctx, *job, req)
	} else {
		out, err = c.CancelWorkflowRecoveryExecution(ctx, *job, req)
	}
	if err != nil {
		return printErr("Recovery cancellation failed", err)
	}
	if jsonOutput {
		_ = jsonOut(writeJSON(out))
	} else {
		_, _ = fmt.Fprintf(osStdout, "%s | item %d | run %s | generation %d | %s\n", oneLine(out.JobID), out.Position, oneLine(out.RunID), out.ResumeCount, oneLine(out.Outcome))
	}
	if out.Outcome != "eligible" && out.Outcome != "cancelled" && out.Outcome != "already_cancelled" {
		return 5
	}
	return 0
}
func writeAutomationRecoveryAttention(w io.Writer, app, job string, attention api.WorkflowRecoveryExecutionAttention) {
	_, _ = fmt.Fprintf(w, "  Execution attention: stalled %d | deadline exceeded %d | capacity waits %d | scheduled waits %d | unknown %d\n", attention.StalledCount, attention.DeadlineExceededCount, attention.CapacityWaitingCount, attention.ScheduledWaitingCount, attention.UnknownCount)
	for _, target := range attention.Targets {
		_, _ = fmt.Fprintf(w, "  Item %d | run %s | generation %d | %s\n    Run: %s\n    Diagnostics: %s\n", target.Position, oneLine(target.RunID), target.ResumeCount, oneLine(target.Status), oneLine(target.RunURL), oneLine(target.DiagnosticsURL))
		if target.Status == "stalled" || target.Status == "deadline_exceeded" {
			_, _ = fmt.Fprintf(w, "    Cancel preview: gregale automation recovery-cancel-preview --app %s --job %s --position %d --resume-count %d --reason 'explain cancellation'\n", automationCommandArgument(app), automationCommandArgument(job), target.Position, target.ResumeCount)
		}
	}
	_, _ = fmt.Fprintf(w, "  Review saved failures: gregale automation recovery-retry-plan --app %s --job %s\n  Audit: gregale event recovery history %s\n", automationCommandArgument(app), automationCommandArgument(job), automationCommandArgument(job))
}
