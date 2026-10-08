package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var workflowUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func cmdWorkflows(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows <list|schedules|run|status|steps|attempts|retry|resume|resumes|cancel|events>", "workflows")
		return 1
	}
	switch args[0] {
	case "list", "ls", "runs":
		return cmdWorkflowsList(args[1:])
	case "schedules":
		return cmdWorkflowSchedules(args[1:])
	case "run":
		return cmdWorkflowsRun(args[1:])
	case "status", "get", "show":
		return cmdWorkflowsStatus(args[1:])
	case "steps":
		return cmdWorkflowsSteps(args[1:])
	case "attempts":
		return cmdWorkflowsAttempts(args[1:])
	case "retry":
		return cmdWorkflowsRetry(args[1:])
	case "resume":
		return cmdWorkflowsResume(args[1:])
	case "resumes":
		return cmdWorkflowsResumes(args[1:])
	case "cancel":
		return cmdWorkflowsCancel(args[1:])
	case "events":
		return cmdWorkflowsEvents(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown workflows subcommand: %s", args[0]), "workflows")
		if parent, ok := lookupCliCommand("workflows"); ok {
			sug, _ := suggestSubcommand(args[0], parent)
			maybeSuggestSub(sug)
		}
		return 1
	}
}

func cmdWorkflowsList(args []string) int {
	fs := newFlagSet("workflows-list", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "page offset")
	status := fs.String("status", "", "filter by status (pending|running|awaiting_event|succeeded|failed|dead)")
	workflowName := fs.String("workflow-name", "", "filter by exact workflow name")
	createdAfter := fs.String("created-after", "", "inclusive RFC3339 creation-time start")
	createdBefore := fs.String("created-before", "", "inclusive RFC3339 creation-time end")
	usage := "usage: gregale workflows list --app <slug> [--limit N] [--offset N] [--status S] [--workflow-name NAME] [--created-after RFC3339] [--created-before RFC3339]"
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}

	if *appSlug == "" {
		PrintUsage(os.Stderr, usage, "workflows")
		return 1
	}
	if err := validateCLILimit("limit", *limit, 100); err != nil {
		PrintUsage(os.Stderr, usage+" (1 <= N <= 100)", "workflows")
		return 1
	}
	if err := validateCLIOffset("offset", *offset); err != nil {
		PrintUsage(os.Stderr, usage+" (offset >= 0)", "workflows")
		return 1
	}
	if !api.ValidWorkflowRunStatus(*status) {
		PrintUsage(os.Stderr, fmt.Sprintf("invalid workflow status %q; want pending, running, awaiting_event, succeeded, failed, or dead", *status), "workflows")
		return 1
	}
	if len(*workflowName) > api.WorkflowWebhookNameMaxBytes {
		return printErr("Invalid --workflow-name", fmt.Errorf("must contain at most %d bytes", api.WorkflowWebhookNameMaxBytes))
	}
	after, err := parseWorkflowRunListTime(*createdAfter, "created-after")
	if err != nil {
		return printErr("Invalid workflow run time filter", err)
	}
	before, err := parseWorkflowRunListTime(*createdBefore, "created-before")
	if err != nil {
		return printErr("Invalid workflow run time filter", err)
	}
	if after != nil && before != nil && after.After(*before) {
		return printErr("Invalid workflow run time range", fmt.Errorf("--created-after must not be later than --created-before"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	out, err := client.ListWorkflowRunsWithOptions(context.Background(), *appSlug, api.WorkflowRunListOptions{
		Limit: *limit, Offset: *offset, Status: *status, WorkflowName: *workflowName,
		CreatedAfter: after, CreatedBefore: before,
	})
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(writeNDJSON(out.Runs))
	}

	renderWorkflowRunsTable(osStdout, out.Runs)
	return 0
}

func parseWorkflowRunListTime(value, flagName string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("--%s must be RFC3339: %w", flagName, err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func cmdWorkflowsRun(args []string) int {
	// The workflow name may come before or after the flags (help shows
	// `workflows run --app <slug> <workflow-name>`).
	flagArgs, positional := splitArgsForFlags(args)
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale workflows run <workflow_name> --app <slug> [--input '{\"k\":\"v\"}'] [--idempotency-key <key>]", "workflows")
		return 1
	}
	workflowName := positional[0]

	fs := newFlagSet("workflows-run", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug")
	inputStr := fs.String("input", "{}", "JSON input payload for the workflow")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying an uncertain run start")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}

	if *appSlug == "" {
		PrintUsage(os.Stderr, "usage: gregale workflows run <workflow_name> --app <slug> [--input '{\"k\":\"v\"}'] [--idempotency-key <key>]", "workflows")
		return 1
	}

	if !json.Valid([]byte(*inputStr)) {
		printCommandValidation(os.Stderr, "error: --input must be valid JSON\n")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	run, err := client.RunWorkflowWithIdempotencyKey(context.Background(), *appSlug, workflowName, json.RawMessage(*inputStr), *idempotencyKey)
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}

	fmt.Printf("Workflow run initiated: %s\n", run.ID)
	fmt.Printf("Status:       %s\n", run.Status)
	fmt.Printf("Workflow:     %s\n", run.WorkflowName)
	return 0
}

func cmdWorkflowsStatus(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows status <run_id>", "workflows")
		return 1
	}
	runID := args[0]
	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	run, err := client.GetWorkflowRun(context.Background(), runID)
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}

	_, _ = fmt.Fprintf(osStdout, "Run ID:       %s\n", run.ID)
	_, _ = fmt.Fprintf(osStdout, "Workflow:     %s\n", run.WorkflowName)
	_, _ = fmt.Fprintf(osStdout, "Status:       %s\n", run.Status)
	_, _ = fmt.Fprintf(osStdout, "Resume Count: %d\n", run.ResumeCount)
	if run.CurrentStep != nil {
		_, _ = fmt.Fprintf(osStdout, "Current Step: %s\n", *run.CurrentStep)
	}
	if run.StartedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Started:      %s\n", *run.StartedAt)
	}
	if run.FinishedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Finished:     %s\n", *run.FinishedAt)
	}
	if run.LastError != nil {
		_, _ = fmt.Fprintf(osStdout, "Last Error:   %s\n", *run.LastError)
	}
	if len(run.Output) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Output:       %s\n", string(run.Output))
	}
	return 0
}

func cmdWorkflowsSteps(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows steps <run_id>", "workflows")
		return 1
	}
	runID := args[0]
	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	resp, err := client.ListWorkflowSteps(context.Background(), runID)
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Steps))
	}

	renderWorkflowStepsTable(osStdout, resp.Steps)
	return 0
}

func cmdWorkflowsAttempts(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale workflows attempts <run_id> <step_name>", "workflows")
		return 1
	}
	runID, stepName := args[0], args[1]
	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}
	if stepName == "" {
		printCommandValidation(os.Stderr, "error: step name is required\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListWorkflowStepAttempts(context.Background(), runID, stepName)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Attempts))
	}
	renderWorkflowStepAttemptsTable(osStdout, resp.Attempts)
	return 0
}

func cmdWorkflowsResume(args []string) int {
	flagArgs, positional := splitArgsForFlags(args)
	usage := "usage: gregale workflows resume <run_id> --expected-resume-count <N> [--idempotency-key <KEY>]"
	if len(positional) != 1 {
		PrintUsage(os.Stderr, usage, "workflows")
		return 1
	}
	runID := positional[0]
	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	fs := newFlagSet("workflows-resume", flag.ContinueOnError)
	expectedResumeCount := fs.Int("expected-resume-count", -1, "current resume_count from workflows status")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying the same resume request")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *expectedResumeCount < 0 || *expectedResumeCount > api.WorkflowRunMaxResumes {
		PrintUsage(os.Stderr, usage+fmt.Sprintf(" (0 <= N <= %d)", api.WorkflowRunMaxResumes), "workflows")
		return 1
	}
	key := strings.TrimSpace(*idempotencyKey)
	if err := validateDeployIdempotencyKey(key); err != nil {
		return printErr("Invalid --idempotency-key", err)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	expected := *expectedResumeCount
	run, err := client.ResumeWorkflowRunWithIdempotencyKey(context.Background(), runID, api.ResumeWorkflowRunRequest{
		ExpectedResumeCount: &expected,
	}, key)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}
	fmt.Printf("Workflow run resumed: %s\n", run.ID)
	fmt.Printf("Status:        %s\n", run.Status)
	fmt.Printf("Resume Count:  %d\n", run.ResumeCount)
	return 0
}

func cmdWorkflowsResumes(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale workflows resumes <run_id>", "workflows")
		return 1
	}
	runID := args[0]
	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListWorkflowResumes(context.Background(), runID)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Resumes))
	}
	renderWorkflowResumesTable(osStdout, resp.Resumes)
	return 0
}

func cmdWorkflowsCancel(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows cancel <run_id>", "workflows")
		return 1
	}
	runID := args[0]
	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	run, err := client.CancelWorkflowRun(context.Background(), runID)
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}

	fmt.Printf("Workflow run %s cancelled (status: %s)\n", run.ID, run.Status)
	return 0
}

func cmdWorkflowsRetry(args []string) int {
	if len(args) != 2 || !workflowUUIDPattern.MatchString(args[0]) || args[1] == "" {
		PrintUsage(os.Stderr, "usage: gregale workflows retry <run_id> <step_name>", "workflows")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	run, err := client.RetryWorkflowStep(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}
	_, _ = fmt.Fprintf(osStdout, "Workflow run %s requeued at step %s (status: %s)\n", run.ID, args[1], run.Status)
	return 0
}

func cmdWorkflowsEvents(args []string) int {
	// The documented form is `workflows events <run_id> <event_name>`; the
	// older `workflows events send ...` spelling keeps working. Requiring
	// "send" made the documented command fail (production-us hunt #5, H5-46).
	if len(args) > 0 && args[0] == "send" {
		args = args[1:]
	}

	fs := newFlagSet("workflows-events-send", flag.ContinueOnError)
	payloadStr := fs.String("payload", "{}", "JSON payload for the event")
	flags, posArgs := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(posArgs) != 2 {
		PrintUsage(os.Stderr, "usage: gregale workflows events <run_id> <event_name> [--payload '{\"k\":\"v\"}']; legacy form: gregale workflows events send <run_id> <event_name>", "workflows")
		return 1
	}

	runID := posArgs[0]
	eventName := posArgs[1]

	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	if !json.Valid([]byte(*payloadStr)) {
		printCommandValidation(os.Stderr, "error: --payload must be valid JSON\n")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	resp, err := client.SendWorkflowEvent(context.Background(), runID, eventName, json.RawMessage(*payloadStr))
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(resp))
	}

	fmt.Printf("Event %q sent to run %s (status: %s)\n", resp.EventName, runID, resp.Status)
	return 0
}

func renderWorkflowRunsTable(w io.Writer, runs []api.WorkflowRunResponse) {
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(w, "No workflow runs found.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-15s  %-20s\n", "RUN ID", "WORKFLOW", "STATUS", "CREATED AT")
	for _, r := range runs {
		_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-15s  %-20s\n", r.ID, r.WorkflowName, r.Status, r.CreatedAt)
	}
}

func renderWorkflowStepsTable(w io.Writer, steps []api.WorkflowStepResponse) {
	if len(steps) == 0 {
		_, _ = fmt.Fprintln(w, "No steps recorded.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-20s  %-15s  %-8s  %-20s\n", "STEP NAME", "STATUS", "ATTEMPT", "CREATED AT")
	for _, s := range steps {
		_, _ = fmt.Fprintf(w, "%-20s  %-15s  %-8d  %-20s\n", s.StepName, s.Status, s.Attempt, s.CreatedAt)
	}
}

func renderWorkflowStepAttemptsTable(w io.Writer, attempts []api.WorkflowStepAttemptResponse) {
	if len(attempts) == 0 {
		_, _ = fmt.Fprintln(w, "No attempts recorded.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-8s  %-10s  %-6s  %-25s  %-25s  %-25s\n", "ATTEMPT", "STATUS", "HTTP", "STARTED AT", "FINISHED AT", "NEXT ATTEMPT")
	for _, a := range attempts {
		httpStatus, finishedAt, nextAttemptAt := "-", "-", "-"
		if a.HTTPStatus != nil {
			httpStatus = fmt.Sprint(*a.HTTPStatus)
		}
		if a.FinishedAt != nil {
			finishedAt = *a.FinishedAt
		}
		if a.NextAttemptAt != nil {
			nextAttemptAt = *a.NextAttemptAt
		}
		_, _ = fmt.Fprintf(w, "%-8d  %-10s  %-6s  %-25s  %-25s  %-25s\n", a.Attempt, a.Status, httpStatus, a.StartedAt, finishedAt, nextAttemptAt)
		if a.Error != nil && *a.Error != "" {
			_, _ = fmt.Fprintf(w, "  error: %s\n", *a.Error)
		}
		for _, effect := range a.Effects {
			_, _ = fmt.Fprintf(w, "  effect %-20s %-12s delivery=%s attempt=%d\n", effect.Name, effect.Status, effect.DeliveryID, effect.Attempt)
			if effect.LastError != "" {
				_, _ = fmt.Fprintf(w, "    error: %s\n", effect.LastError)
			}
		}
	}
}

func renderWorkflowResumesTable(w io.Writer, resumes []api.WorkflowResumeResponse) {
	if len(resumes) == 0 {
		_, _ = fmt.Fprintln(w, "No resume history recorded.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-8s  %-15s  %-32s  %-25s\n", "RESUME", "PREVIOUS STATUS", "RESUMED STEPS", "CREATED AT")
	for _, resume := range resumes {
		_, _ = fmt.Fprintf(w, "%-8d  %-15s  %-32s  %-25s\n", resume.ResumeNumber, resume.PreviousStatus, strings.Join(resume.ResumedSteps, ","), resume.CreatedAt)
	}
}
