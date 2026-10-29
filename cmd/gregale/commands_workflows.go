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
		PrintUsage(os.Stderr, "usage: gregale workflows <defaults|list|schedules|schedule-history|run|wait|status|diagnose|steps|attempts|retry|resume|resumes|cancel|cancel-queued-preview|cancel-queued|events>", "workflows")
		return 1
	}
	subcommand, rest := args[0], args[1:]
	switch subcommand {
	case "defaults":
		return cmdWorkflowsDefaults(rest)
	case "list", "ls", "runs":
		return cmdWorkflowsList(rest)
	case "schedules":
		return cmdWorkflowSchedules(rest)
	case "schedule-history":
		return cmdWorkflowScheduleHistory(rest)
	case "run":
		return cmdWorkflowsRun(rest)
	case "resume-wait":
		return resumeRecordedRun("workflow", rest)
	case "wait":
		return cmdWorkflowsWait(rest)
	case "diagnose":
		return cmdWorkflowsDiagnose(rest)
	case "status", "get", "show":
		return cmdWorkflowsStatus(rest)
	case "steps":
		return cmdWorkflowsSteps(rest)
	case "attempts":
		return cmdWorkflowsAttempts(rest)
	case "retry":
		return cmdWorkflowsRetry(rest)
	case "resume":
		return cmdWorkflowsResume(rest)
	case "resumes":
		return cmdWorkflowsResumes(rest)
	case "cancel":
		return cmdWorkflowsCancel(rest)
	case "cancel-queued-preview":
		return cmdWorkflowsQueuedCancel(rest, false)
	case "cancel-queued":
		return cmdWorkflowsQueuedCancel(rest, true)
	case "events":
		return cmdWorkflowsEvents(rest)
	default:
		printCommandValidation(os.Stderr, "unknown workflows subcommand %q\n", subcommand)
		return unknownSubcommandChoices(os.Stderr, "workflows", nestedCliCommand("workflows"), subcommand)
	}
}

func cmdWorkflowsList(args []string) int {
	fs := newFlagSet("workflows-list", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (defaults to linked project app)")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "page offset")
	all := fs.Bool("all", false, "walk all history pages from the selected offset")
	status := fs.String("status", "", "filter by status (pending|running|awaiting_event|succeeded|failed|dead)")
	workflowName := fs.String("workflow-name", "", "filter by exact workflow name")
	createdAfter := fs.String("created-after", "", "inclusive RFC3339 creation-time start")
	createdBefore := fs.String("created-before", "", "inclusive RFC3339 creation-time end")
	usage := "usage: gregale workflow list [--app <slug>] [--limit N] [--offset N] [--all] [--status S] [--workflow-name NAME] [--created-after RFC3339] [--created-before RFC3339]"
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}

	resolvedApp, appErr := resolveWorkflowApp(*appSlug)
	if appErr != nil {
		return printErr("Could not resolve workflow app", appErr)
	}
	*appSlug = resolvedApp
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

	opts := api.WorkflowRunListOptions{
		Limit: *limit, Offset: *offset, Status: *status, WorkflowName: *workflowName,
		CreatedAfter: after, CreatedBefore: before,
	}
	items, next, err := collectWorkflowRunPages(client, *appSlug, opts, *all)
	if err != nil {
		return printErr("Could not list workflow runs", err)
	}

	if jsonOutput {
		return jsonOut(writeNDJSON(items))
	}

	if len(items) == 0 {
		detail := fmt.Sprintf("offset=%d; status=%q; workflow=%q; created-after=%q; created-before=%q (blank filters are unrestricted)", *offset, *status, *workflowName, *createdAfter, *createdBefore)
		printEmptyResult("workflow runs in app "+*appSlug, detail, recoveryCLI()+" workflow list --app "+recoveryArgument(*appSlug))
		_, _ = fmt.Fprintln(osStdout, "The next command starts at the first page without these filters. No matches does not establish whether an uncertain submission was accepted.")
		return 0
	}
	renderWorkflowRunsTable(osStdout, items)
	printFailedWorkflowRunCommands(osStdout, items)
	printActiveWorkflowRunCommands(osStdout, items)
	if next >= 0 {
		opts.Offset = next
		_, _ = fmt.Fprintf(osStdout, "Next page (POSIX shells):\n  %s\n", workflowListNextCommand(*appSlug, opts))
	}
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
	flagArgs, positional := splitArgsForFlags(args, "wait")
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale workflow run <workflow_name> [--app <slug>] [--input JSON | --input-file PATH|-] [--idempotency-key <key>] [--wait [--timeout 5m] [--poll-interval 2s]]", "workflows")
		return 1
	}
	workflowName := positional[0]

	fs := newFlagSet("workflows-run", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (defaults to linked project app)")
	inputStr := fs.String("input", "{}", "JSON input payload for the workflow")
	inputFile := fs.String("input-file", "", "read JSON input from a file, or - for stdin (excludes --input)")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying an uncertain run start")
	wait := fs.Bool("wait", false, "wait for the accepted run to succeed")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum wait after acceptance (requires --wait)")
	interval := fs.Duration("poll-interval", 2*time.Second, "wait poll interval (requires --wait; 1s to 1h)")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	waitOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" || f.Name == "poll-interval" {
			waitOptions = true
		}
	})
	if (waitOptions && !*wait) || (*wait && (*timeout <= 0 || *interval < time.Second || *interval > time.Hour)) {
		return printErr("Invalid workflow wait options", fmt.Errorf("--timeout and --poll-interval require --wait; use a positive timeout and a poll interval between 1s and 1h"))
	}

	resolvedApp, appErr := resolveWorkflowApp(*appSlug)
	if appErr != nil {
		return printErr("Could not resolve workflow app", appErr)
	}
	appSource := targetValueSource(*appSlug, resolvedApp)
	*appSlug = resolvedApp

	var inlineSet, fileSet bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "input" {
			inlineSet = true
		}
		if f.Name == "input-file" {
			fileSet = true
		}
	})
	input, inputErr := workflowRunInput(*inputStr, *inputFile, inlineSet, fileSet)
	if inputErr != nil {
		printCommandValidation(osStderr, "%s\n", inputErr)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	printWorkflowRunTarget(*appSlug, appSource)
	run, err := client.RunWorkflowWithIdempotencyKey(context.Background(), *appSlug, workflowName, input, *idempotencyKey)
	if err != nil {
		return printRunSubmissionError("Request failed", err, recoveryCLI()+" workflow list --app "+recoveryArgument(*appSlug)+" --workflow-name "+recoveryArgument(workflowName), *idempotencyKey != "")
	}
	rememberAcceptedWorkflow(*appSlug, workflowName, run)
	if *wait {
		if !workflowUUIDPattern.MatchString(run.ID) {
			if code := renderWorkflowRunStatus(run); code != 0 {
				return code
			}
			return printErr("Cannot wait for accepted workflow", fmt.Errorf("submission response did not include a valid run ID; inspect workflows list --app %s --workflow-name %s before submitting again", recoveryArgument(*appSlug), recoveryArgument(workflowName)))
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Workflow run accepted: %s. Waiting for this exact run.\n", run.ID)
		}
		return waitWorkflowRun(client, run.ID, *timeout, *interval, &run)
	}

	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}

	_, _ = fmt.Fprintf(osStdout, "Workflow run accepted: %s\nApp: %s\nWorkflow: %s\nStatus: %s\n", run.ID, oneLine(*appSlug), oneLine(run.WorkflowName), oneLine(run.Status))
	printWorkflowRunNextSteps(run.ID, run.Status)
	return 0
}

func cmdWorkflowsStatus(args []string) int {
	if workflowsInteractiveRequested(args) {
		return cmdWorkflowsInteractive("status", args)
	}
	fs := newFlagSet("workflows status", flag.ContinueOnError)
	app := fs.String("app", "", "app slug for latest (defaults to linked project app)")
	name := fs.String("workflow-name", "", "exact workflow name required for latest")
	check := fs.Bool("check", false, "exit successfully only for a succeeded, non-cancelled run")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || (fs.Arg(0) != "latest" && !workflowUUIDPattern.MatchString(fs.Arg(0))) {
		printCommandValidation(osStderr, "error: invalid run ID or arguments; %s\n", "usage: gregale workflow status RUN_ID|latest [--workflow-name NAME] [--app SLUG] [--check]")
		return 1
	}
	target, code := validateWorkflowReadTarget(fs.Arg(0), *app, *name)
	if code != 0 {
		return code
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx := context.Background()
	if target.Ref == "latest" || *check {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveWorkflowReadTarget(ctx, client, target)
	if err != nil {
		return printErr("Could not resolve workflow run", err)
	}
	run, err := client.GetWorkflowRun(ctx, runID)
	if err != nil {
		return printErr("Could not read workflow run", err)
	}
	if *check && run.ID != runID {
		return printErr("Invalid workflow run", fmt.Errorf("status response did not identify the requested run"))
	}
	if code := renderWorkflowRunStatus(run); code != 0 {
		return code
	}
	if *check && (run.Status != "succeeded" || run.CancelledAt != nil) {
		return printErr("Workflow run check failed", fmt.Errorf("run %s has recorded status %s (cancelled=%t); only a succeeded, non-cancelled run passes --check", runID, oneLine(run.Status), run.CancelledAt != nil))
	}
	return 0
}

func renderWorkflowRunStatus(run api.WorkflowRunResponse) int {
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}

	_, _ = fmt.Fprintf(osStdout, "Run ID:       %s\n", run.ID)
	_, _ = fmt.Fprintf(osStdout, "Workflow:     %s\n", run.WorkflowName)
	_, _ = fmt.Fprintf(osStdout, "Deployment:   %s\n", workflowRunDeploymentLabel(run))
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
	printWorkflowFailureEvidence(osStdout, run)
	printActiveWorkflowRunCommands(osStdout, []api.WorkflowRunResponse{run})
	return 0
}

func cmdWorkflowsSteps(args []string) int {
	fs := newFlagSet("workflows steps", flag.ContinueOnError)
	app := fs.String("app", "", "app slug for latest (defaults to linked project app)")
	name := fs.String("workflow-name", "", "exact workflow name required for latest")
	failedOnly := fs.Bool("failed-only", false, "show only failed steps")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || (fs.Arg(0) != "latest" && !workflowUUIDPattern.MatchString(fs.Arg(0))) {
		printCommandValidation(osStderr, "error: invalid run ID or arguments; %s\n", "usage: gregale workflow steps RUN_ID|latest [--workflow-name NAME] [--app SLUG] [--failed-only]")
		return 1
	}
	target, code := validateWorkflowReadTarget(fs.Arg(0), *app, *name)
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
	resp, err := client.ListWorkflowSteps(ctx, runID)
	if err != nil {
		return printErr("Could not list workflow steps", err)
	}

	if *failedOnly {
		failed := make([]api.WorkflowStepResponse, 0)
		for _, step := range resp.Steps {
			if step.Status == "failed" {
				failed = append(failed, step)
			}
		}
		resp.Steps = failed
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Steps))
	}

	if *failedOnly && len(resp.Steps) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No failed steps found for workflow run %s. Step states may still change.\n", runID)
		return 0
	}
	renderWorkflowStepsTable(osStdout, resp.Steps)
	if *failedOnly {
		_, _ = fmt.Fprintf(osStdout, "Inspect run diagnostics (POSIX shells):\n  %s workflows diagnose %s\nInspect failed step attempts:\n", recoveryCLI(), recoveryArgument(runID))
		for _, step := range resp.Steps {
			_, _ = fmt.Fprintf(osStdout, "  %s workflows attempts %s %s\n", recoveryCLI(), recoveryArgument(runID), recoveryArgument(step.StepName))
		}
	}
	return 0
}

func cmdWorkflowsAttempts(args []string) int {
	fs := newFlagSet("workflows attempts", flag.ContinueOnError)
	app := fs.String("app", "", "app slug for latest (defaults to linked project app)")
	name := fs.String("workflow-name", "", "exact workflow name required for latest")
	failedOnly := fs.Bool("failed-only", false, "show only failed attempts")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale workflow attempts <run_id|latest> <step_name> [--workflow-name NAME] [--app SLUG] [--failed-only]", "workflows")
		return 1
	}
	ref, stepName := fs.Arg(0), fs.Arg(1)
	if ref != "latest" && !workflowUUIDPattern.MatchString(ref) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID or latest)\n", ref)
		return 1
	}
	if stepName == "" {
		printCommandValidation(os.Stderr, "error: step name is required\n")
		return 1
	}
	target, code := validateWorkflowReadTarget(ref, *app, *name)
	if code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if ref == "latest" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	runID, err := resolveWorkflowReadTarget(ctx, client, target)
	if err != nil {
		return printErr("Could not resolve workflow run", err)
	}
	resp, err := client.ListWorkflowStepAttempts(ctx, runID, stepName)
	if err != nil {
		return printErr("Could not list workflow step attempts", err)
	}
	if *failedOnly {
		failed := make([]api.WorkflowStepAttemptResponse, 0)
		for _, attempt := range resp.Attempts {
			if attempt.Status == "failed" {
				failed = append(failed, attempt)
			}
		}
		resp.Attempts = failed
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Attempts))
	}
	if *failedOnly && len(resp.Attempts) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No failed attempts found for workflow run %s step %s. Attempt states and retained history may change.\n", runID, oneLine(stepName))
		return 0
	}
	renderWorkflowStepAttemptsTable(osStdout, resp.Attempts)
	return 0
}

func cmdWorkflowsResume(args []string) int {
	flagArgs, positional := splitArgsForFlags(args, "wait")
	usage := "usage: gregale workflow resume <run_id> --expected-resume-count <N> [--idempotency-key <KEY>] [--wait [--timeout 5m] [--poll-interval 2s]]"
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
	wait := fs.Bool("wait", false, "wait for the resumed run to succeed")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum wait after resume acceptance (requires --wait)")
	interval := fs.Duration("poll-interval", 2*time.Second, "wait poll interval (requires --wait; 1s to 1h)")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	waitOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" || f.Name == "poll-interval" {
			waitOptions = true
		}
	})
	if (waitOptions && !*wait) || (*wait && (*timeout <= 0 || *interval < time.Second || *interval > time.Hour)) {
		return printErr("Invalid workflow wait options", fmt.Errorf("--timeout and --poll-interval require --wait; use a positive timeout and a poll interval between 1s and 1h"))
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
		return printErr("Could not resume workflow run", err)
	}
	if run.ID == runID {
		rememberAcceptedWorkflow("", "", run)
	}
	if *wait {
		if run.ID != runID {
			if code := renderWorkflowRunStatus(run); code != 0 {
				return code
			}
			return printErr("Cannot wait for resumed workflow", fmt.Errorf("resume response did not identify the requested run; inspect %s workflows status %s and its resume history before repeating the resume request", recoveryCLI(), recoveryArgument(runID)))
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Workflow resume accepted: %s (resume count=%d). Waiting for this exact run.\n", run.ID, run.ResumeCount)
		}
		return waitWorkflowRun(client, runID, *timeout, *interval, &run)
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
	target, code := parseWorkflowReadTarget("resumes", args)
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
	resp, err := client.ListWorkflowResumes(ctx, runID)
	if err != nil {
		return printErr("Could not list workflow resumes", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Resumes))
	}
	renderWorkflowResumesTable(osStdout, resp.Resumes)
	return 0
}

func cmdWorkflowsCancel(args []string) int {
	if workflowsInteractiveRequested(args) {
		return cmdWorkflowsInteractive("cancel", args)
	}
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow cancel <run_id>", "workflows")
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
		return printErr("Could not cancel workflow run", err)
	}

	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}

	if run.CancelledAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Workflow run %s cancelled (status: %s)\n", run.ID, oneLine(run.Status))
	} else {
		_, _ = fmt.Fprintf(osStdout, "Workflow run %s was already finished; no cancellation applied (status: %s)\n", run.ID, oneLine(run.Status))
	}
	printWorkflowRunNextSteps(run.ID, run.Status)
	return 0
}

func cmdWorkflowsRetry(args []string) int {
	if workflowsInteractiveRequested(args) {
		return cmdWorkflowsInteractive("retry", args)
	}
	flagArgs, positional := splitArgsForFlags(args, "wait")
	usage := "usage: gregale workflow retry <run_id> <step_name> [--wait [--timeout 5m] [--poll-interval 2s]]"
	if len(positional) != 2 || !workflowUUIDPattern.MatchString(positional[0]) || positional[1] == "" {
		PrintUsage(os.Stderr, usage, "workflows")
		return 1
	}
	fs := newFlagSet("workflows-retry", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the retried run to succeed")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum wait after retry acceptance (requires --wait)")
	interval := fs.Duration("poll-interval", 2*time.Second, "wait poll interval (requires --wait; 1s to 1h)")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	waitOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" || f.Name == "poll-interval" {
			waitOptions = true
		}
	})
	if (waitOptions && !*wait) || (*wait && (*timeout <= 0 || *interval < time.Second || *interval > time.Hour)) {
		return printErr("Invalid workflow wait options", fmt.Errorf("--timeout and --poll-interval require --wait; use a positive timeout and a poll interval between 1s and 1h"))
	}
	runID, stepName := positional[0], positional[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	run, err := client.RetryWorkflowStep(context.Background(), runID, stepName)
	if err != nil {
		return printErr("Could not retry workflow step", err)
	}
	if run.ID == runID {
		rememberAcceptedWorkflow("", "", run)
	}
	if *wait {
		if run.ID != runID {
			if code := renderWorkflowRunStatus(run); code != 0 {
				return code
			}
			return printErr("Cannot wait for retried workflow", fmt.Errorf("retry response did not identify the requested run; inspect %s workflows status %s and its step attempts before repeating the retry request", recoveryCLI(), recoveryArgument(runID)))
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Workflow retry accepted: %s (step=%s). Waiting for this exact run.\n", run.ID, oneLine(stepName))
		}
		return waitWorkflowRun(client, runID, *timeout, *interval, &run)
	}
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(run))
	}
	_, _ = fmt.Fprintf(osStdout, "Workflow run %s requeued at step %s (status: %s)\n", run.ID, oneLine(stepName), oneLine(run.Status))
	printWorkflowRunNextSteps(run.ID, run.Status)
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
	payloadFile := fs.String("payload-file", "", "read JSON payload from a file, or - for stdin (excludes --payload)")
	flags, posArgs := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(posArgs) != 2 {
		PrintUsage(os.Stderr, "usage: gregale workflow events <run_id> <event_name> [--payload JSON | --payload-file PATH|-]; legacy form: gregale workflow events send <run_id> <event_name>", "workflows")
		return 1
	}

	runID := posArgs[0]
	eventName := posArgs[1]

	if !workflowUUIDPattern.MatchString(runID) {
		printCommandValidation(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	var inlineSet, fileSet bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "payload" {
			inlineSet = true
		}
		if f.Name == "payload-file" {
			fileSet = true
		}
	})
	payload, payloadErr := workflowEventPayload(*payloadStr, *payloadFile, inlineSet, fileSet, eventName)
	if payloadErr != nil {
		printCommandValidation(osStderr, "%s\n", payloadErr)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	resp, err := client.SendWorkflowEvent(context.Background(), runID, eventName, payload)
	if err != nil {
		return printErr("Could not send workflow event", err)
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
	_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-15s  %-36s  %-20s\n", "RUN ID", "WORKFLOW", "STATUS", "DEPLOYMENT", "CREATED AT")
	for _, r := range runs {
		_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-15s  %-36s  %-20s\n", r.ID, r.WorkflowName, r.Status, workflowRunDeploymentLabel(r), r.CreatedAt)
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

func workflowRunDeploymentLabel(r api.WorkflowRunResponse) string {
	if r.DeploymentID == "" {
		return "legacy (unpinned)"
	}
	return r.DeploymentID
}
