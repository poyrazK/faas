package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowsDiagnose(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale workflows diagnose <run_id>", "workflows")
		return 1
	}
	if !workflowUUIDPattern.MatchString(args[0]) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", args[0])
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetWorkflowRunDiagnostics(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
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
	_, _ = fmt.Fprintln(w, "\nSTEP  KIND  STATUS  ATTEMPT")
	for _, step := range result.Steps {
		_, _ = fmt.Fprintf(w, "%s  %s  %s  %d\n", step.StepName, step.Kind, step.Status, step.Attempt)
	}
}
