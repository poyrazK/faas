package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowsDiagnose(args []string) int {
	target, code := parseWorkflowReadTarget("diagnose", args)
	if code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if target.Ref == "latest" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveWorkflowReadTarget(ctx, client, target)
	if err != nil {
		return printErr("Could not resolve workflow run", err)
	}
	result, err := client.GetWorkflowRunDiagnostics(ctx, runID)
	if err != nil {
		return printErr("Could not diagnose workflow run", err)
	}
	if target.Ref == "latest" && result.RunID != runID {
		return printErr("Could not diagnose selected workflow run", fmt.Errorf("diagnostics response did not identify the selected run %s", runID))
	}
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(result))
	}
	renderWorkflowRunDiagnostics(osStdout, result)
	return 0
}

func renderWorkflowRunDiagnostics(w io.Writer, result api.WorkflowRunDiagnosticsResponse) {
	_, _ = fmt.Fprintf(w, "Run ID:       %s\nWorkflow:     %s\nStatus:       %s\nReason:       %s\nObserved:     %s\n", result.RunID, result.WorkflowName, result.Status, result.StateReason, result.ObservedAt)
	deployment := result.DeploymentID
	if result.LegacyUnpinned {
		deployment = "legacy (unpinned)"
	}
	_, _ = fmt.Fprintf(w, "Deployment:   %s\nDue age:      %.0f seconds\nStale lease:  %t\n", deployment, result.DueAgeSeconds, result.StaleLease)
	if result.NextWakeAt != nil {
		_, _ = fmt.Fprintf(w, "Next wake:    %s\n", *result.NextWakeAt)
	}
	_, _ = fmt.Fprintf(w, "\nResume eligible: %t\nResume count:    %d\nWould reopen:   %s\nPreserved:      %s\n", result.Resume.Eligible, result.Resume.ExpectedResumeCount, strings.Join(result.Resume.ReopenedSteps, ", "), strings.Join(result.Resume.PreservedSteps, ", "))
	for _, blocker := range result.Resume.Blockers {
		_, _ = fmt.Fprintf(w, "Blocked:        %s [%s]", blocker.Message, blocker.Code)
		if blocker.StepName != "" {
			_, _ = fmt.Fprintf(w, " (step %s)", blocker.StepName)
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintln(w, "Preview only; resume rechecks the current state and capacity.")
	if result.Resume.Eligible && len(result.Resume.Blockers) == 0 && result.Resume.ExpectedResumeCount >= 0 && workflowUUIDPattern.MatchString(result.RunID) {
		_, _ = fmt.Fprintf(w, "\nResume command: gregale workflows resume %s --expected-resume-count %d\n", result.RunID, result.Resume.ExpectedResumeCount)
		_, _ = fmt.Fprintln(w, "Review the reopened steps before running this command; completed effects remain preserved.")
	}
	_, _ = fmt.Fprintln(w, "\nSTEP  KIND  STATUS  ATTEMPT")
	for _, step := range result.Steps {
		_, _ = fmt.Fprintf(w, "%s  %s  %s  %d\n", step.StepName, step.Kind, step.Status, step.Attempt)
		if step.RetryEligible {
			_, _ = fmt.Fprintf(w, "  Manual HTTP retry eligible at observation; skipped descendants to reopen: %s\n", oneLine(strings.Join(step.RetryReopenedSteps, ", ")))
		}
	}
}
