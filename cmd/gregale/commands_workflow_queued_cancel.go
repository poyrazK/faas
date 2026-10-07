package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowsQueuedCancel(args []string, execute bool) int {
	command := "workflows-cancel-queued-preview"
	if execute {
		command = "workflows-cancel-queued"
	}
	flags := newFlagSet(command, flag.ContinueOnError)
	app := flags.String("app", "", "app slug")
	workflowName := flags.String("workflow-name", "", "require an exact workflow name")
	var runIDs stringListFlag
	flags.Var(&runIDs, "run-id", "selected workflow run UUID (repeat up to 20 times)")
	var confirmed *bool
	if execute {
		confirmed = flags.Bool("yes", false, "confirm cancellation of eligible selected runs")
	}
	if flags.Parse(args) != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if *app == "" || len(runIDs) == 0 || len(runIDs) > api.WorkflowQueuedRunCancelBatchMax {
		return printErr("Invalid queued-run selection", fmt.Errorf("--app is required and --run-id must be repeated 1-%d times", api.WorkflowQueuedRunCancelBatchMax))
	}
	if len(*workflowName) > api.WorkflowWebhookNameMaxBytes {
		return printErr("Invalid workflow name", fmt.Errorf("--workflow-name must contain at most %d bytes", api.WorkflowWebhookNameMaxBytes))
	}
	if execute && !*confirmed {
		return printErr("Confirmation required", fmt.Errorf("pass --yes after reviewing workflows cancel-queued-preview"))
	}
	seen := make(map[string]struct{}, len(runIDs))
	for i, rawID := range runIDs {
		id, err := uuid.Parse(rawID)
		if err != nil || id == uuid.Nil {
			return printErr("Invalid run ID", fmt.Errorf("%q is not a UUID", rawID))
		}
		runID := id.String()
		if _, exists := seen[runID]; exists {
			return printErr("Invalid queued-run selection", fmt.Errorf("run %s was selected more than once", runID))
		}
		seen[runID] = struct{}{}
		runIDs[i] = runID
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	request := api.WorkflowQueuedRunCancelRequest{WorkflowName: *workflowName, RunIDs: runIDs}
	var response api.WorkflowQueuedRunCancelResponse
	if execute {
		response, err = client.CancelUnstartedWorkflowRuns(context.Background(), *app, request)
	} else {
		response, err = client.PreviewUnstartedWorkflowRunCancellations(context.Background(), *app, request)
	}
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(response))
	}
	if err := renderWorkflowQueuedRunCancelOutcomes(response.Outcomes); err != nil {
		return printErr("Output failed", err)
	}
	if !execute {
		fmt.Fprintln(osStdout, "Preview only. Recheck the same selection with workflows cancel-queued --yes to cancel eligible runs.")
	}
	return 0
}

func renderWorkflowQueuedRunCancelOutcomes(outcomes []api.WorkflowQueuedRunCancelOutcome) error {
	writer := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "RUN ID\tOUTCOME\tWORKFLOW\tSTATUS"); err != nil {
		return err
	}
	for _, outcome := range outcomes {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", outcome.RunID, outcome.Outcome, outcome.WorkflowName, outcome.Status); err != nil {
			return err
		}
	}
	return writer.Flush()
}
