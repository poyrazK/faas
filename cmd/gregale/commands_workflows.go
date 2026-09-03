package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/onebox-faas/faas/pkg/api"
)

var workflowUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func cmdWorkflows(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows <list|run|status|steps|cancel|events>", "workflows")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdWorkflowsList(args[1:])
	case "run":
		return cmdWorkflowsRun(args[1:])
	case "status":
		return cmdWorkflowsStatus(args[1:])
	case "steps":
		return cmdWorkflowsSteps(args[1:])
	case "cancel":
		return cmdWorkflowsCancel(args[1:])
	case "events":
		return cmdWorkflowsEvents(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown workflows subcommand: %s", args[0]), "workflows")
		return 1
	}
}

func cmdWorkflowsList(args []string) int {
	fs := flag.NewFlagSet("workflows-list", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "page offset")
	status := fs.String("status", "", "filter by status (pending|running|awaiting_event|succeeded|failed|dead)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *appSlug == "" {
		PrintUsage(os.Stderr, "usage: gregale workflows list --app <slug> [--limit N] [--offset N] [--status S]", "workflows")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	out, err := client.ListWorkflowRuns(context.Background(), *appSlug, *limit, *offset, *status)
	if err != nil {
		return printErr("Request failed", err)
	}

	if jsonOutput {
		return jsonOut(writeNDJSON(out.Runs))
	}

	renderWorkflowRunsTable(osStdout, out.Runs)
	return 0
}

func cmdWorkflowsRun(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows run <workflow_name> --app <slug> [--input '{\"k\":\"v\"}']", "workflows")
		return 1
	}
	workflowName := args[0]

	fs := flag.NewFlagSet("workflows-run", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug")
	inputStr := fs.String("input", "{}", "JSON input payload for the workflow")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}

	if *appSlug == "" {
		PrintUsage(os.Stderr, "usage: gregale workflows run <workflow_name> --app <slug> [--input '{\"k\":\"v\"}']", "workflows")
		return 1
	}

	if !json.Valid([]byte(*inputStr)) {
		fmt.Fprintln(os.Stderr, "error: --input must be valid JSON")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	run, err := client.RunWorkflow(context.Background(), *appSlug, workflowName, json.RawMessage(*inputStr))
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
		fmt.Fprintf(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
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

	fmt.Printf("Run ID:       %s\n", run.ID)
	fmt.Printf("Workflow:     %s\n", run.WorkflowName)
	fmt.Printf("Status:       %s\n", run.Status)
	if run.CurrentStep != nil {
		fmt.Printf("Current Step: %s\n", *run.CurrentStep)
	}
	if run.StartedAt != nil {
		fmt.Printf("Started:      %s\n", *run.StartedAt)
	}
	if run.FinishedAt != nil {
		fmt.Printf("Finished:     %s\n", *run.FinishedAt)
	}
	if run.LastError != nil {
		fmt.Printf("Last Error:   %s\n", *run.LastError)
	}
	if len(run.Output) > 0 {
		fmt.Printf("Output:       %s\n", string(run.Output))
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
		fmt.Fprintf(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
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

func cmdWorkflowsCancel(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflows cancel <run_id>", "workflows")
		return 1
	}
	runID := args[0]
	if !workflowUUIDPattern.MatchString(runID) {
		fmt.Fprintf(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
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

func cmdWorkflowsEvents(args []string) int {
	if len(args) == 0 || args[0] != "send" {
		PrintUsage(os.Stderr, "usage: gregale workflows events send <run_id> <event_name> [--payload '{\"k\":\"v\"}']", "workflows")
		return 1
	}

	fs := flag.NewFlagSet("workflows-events-send", flag.ContinueOnError)
	payloadStr := fs.String("payload", "{}", "JSON payload for the event")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}

	posArgs := fs.Args()
	if len(posArgs) < 2 {
		PrintUsage(os.Stderr, "usage: gregale workflows events send <run_id> <event_name> [--payload '{\"k\":\"v\"}']", "workflows")
		return 1
	}

	runID := posArgs[0]
	eventName := posArgs[1]

	if !workflowUUIDPattern.MatchString(runID) {
		fmt.Fprintf(os.Stderr, "error: invalid run ID %q (expected UUID)\n", runID)
		return 1
	}

	if !json.Valid([]byte(*payloadStr)) {
		fmt.Fprintln(os.Stderr, "error: --payload must be valid JSON")
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
		fmt.Fprintln(w, "No workflow runs found.")
		return
	}
	fmt.Fprintf(w, "%-36s  %-20s  %-15s  %-20s\n", "RUN ID", "WORKFLOW", "STATUS", "CREATED AT")
	for _, r := range runs {
		fmt.Fprintf(w, "%-36s  %-20s  %-15s  %-20s\n", r.ID, r.WorkflowName, r.Status, r.CreatedAt)
	}
}

func renderWorkflowStepsTable(w io.Writer, steps []api.WorkflowStepResponse) {
	if len(steps) == 0 {
		fmt.Fprintln(w, "No steps recorded.")
		return
	}
	fmt.Fprintf(w, "%-20s  %-15s  %-8s  %-20s\n", "STEP NAME", "STATUS", "ATTEMPT", "CREATED AT")
	for _, s := range steps {
		fmt.Fprintf(w, "%-20s  %-15s  %-8d  %-20s\n", s.StepName, s.Status, s.Attempt, s.CreatedAt)
	}
}
