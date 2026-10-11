package main

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowScheduleHistory(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "replay-preview":
			return cmdWorkflowScheduleReplay(args[1:], false)
		case "replay":
			return cmdWorkflowScheduleReplay(args[1:], true)
		}
	}
	flags := newFlagSet("workflows-schedule-history", flag.ContinueOnError)
	app := flags.String("app", "", "app slug")
	tenant := flags.String("platform-tenant-id", "", "filter by tenant UUID")
	cursor := flags.String("cursor", "", "next cursor from the previous page")
	limit := flags.Int("limit", api.WorkflowScheduleHistoryPageDefault, "maximum occurrences")
	if flags.Parse(args) != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if *app == "" || *limit <= 0 || *limit > api.WorkflowScheduleHistoryPageMax {
		return printErr("Invalid history options", fmt.Errorf("app is required and limit must be 1-%d", api.WorkflowScheduleHistoryPageMax))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.ListWorkflowScheduleOccurrences(context.Background(), *app, api.ListWorkflowScheduleOccurrencesOptions{PlatformTenantID: *tenant, Cursor: *cursor, Limit: *limit})
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	writer := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SCHEDULED FOR\tWORKFLOW\tTENANT\tSTATUS\tRUN\tREPLAY RUN"); err != nil {
		return printErr("Output failed", err)
	}
	for _, row := range response.Occurrences {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", row.ScheduledFor.UTC().Format(time.RFC3339), row.WorkflowName, row.PlatformTenantID, row.Status, row.RunID, row.ReplayRunID); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return printErr("Output failed", err)
	}
	if response.NextCursor != "" {
		if _, err := fmt.Fprintf(osStdout, "Next cursor: %s\n", response.NextCursor); err != nil {
			return printErr("Output failed", err)
		}
	}
	return 0
}

func cmdWorkflowScheduleReplay(args []string, execute bool) int {
	command := "workflows-schedule-history-replay-preview"
	if execute {
		command = "workflows-schedule-history-replay"
	}
	flags := newFlagSet(command, flag.ContinueOnError)
	app := flags.String("app", "", "app slug")
	var occurrenceIDs stringListFlag
	flags.Var(&occurrenceIDs, "occurrence-id", "skipped occurrence UUID (repeat up to 20 times)")
	if flags.Parse(args) != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if *app == "" || len(occurrenceIDs) == 0 || len(occurrenceIDs) > api.WorkflowScheduleReplayBatchMax {
		return printErr("Invalid replay selection", fmt.Errorf("app is required and --occurrence-id must be repeated 1-%d times", api.WorkflowScheduleReplayBatchMax))
	}
	seen := make(map[string]struct{}, len(occurrenceIDs))
	for i, rawID := range occurrenceIDs {
		id, parseErr := uuid.Parse(rawID)
		if parseErr != nil || id == uuid.Nil {
			return printErr("Invalid occurrence ID", fmt.Errorf("%q is not a UUID", rawID))
		}
		occurrenceID := id.String()
		if _, duplicate := seen[occurrenceID]; duplicate {
			return printErr("Invalid replay selection", fmt.Errorf("occurrence %s was selected more than once", occurrenceID))
		}
		seen[occurrenceID] = struct{}{}
		occurrenceIDs[i] = occurrenceID
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	request := api.WorkflowScheduleReplayRequest{OccurrenceIDs: occurrenceIDs}
	var response api.WorkflowScheduleReplayResponse
	if execute {
		response, err = client.ReplayWorkflowScheduleOccurrences(context.Background(), *app, request)
	} else {
		response, err = client.PreviewWorkflowScheduleReplays(context.Background(), *app, request)
	}
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	writer := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SCHEDULED FOR\tWORKFLOW\tTENANT\tOCCURRENCE\tOUTCOME\tREPLAY RUN"); err != nil {
		return printErr("Output failed", err)
	}
	for _, row := range response.Outcomes {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", row.ScheduledFor, row.WorkflowName, row.PlatformTenantID, row.OccurrenceID, row.Outcome, row.ReplayRunID); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}
