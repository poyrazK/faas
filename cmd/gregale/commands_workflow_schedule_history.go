package main

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowScheduleHistory(args []string) int {
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
	if _, err := fmt.Fprintln(writer, "SCHEDULED FOR\tWORKFLOW\tTENANT\tSTATUS\tRUN"); err != nil {
		return printErr("Output failed", err)
	}
	for _, row := range response.Occurrences {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", row.ScheduledFor.UTC().Format(time.RFC3339), row.WorkflowName, row.PlatformTenantID, row.Status, row.RunID); err != nil {
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
