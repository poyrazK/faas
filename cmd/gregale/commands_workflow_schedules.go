package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowSchedules(args []string) int {
	if len(args) > 0 && args[0] == "preview" {
		return cmdWorkflowSchedulePreview(args[1:])
	}
	flags := newFlagSet("workflows-schedules", flag.ContinueOnError)
	app := flags.String("app", "", "app slug")
	if flags.Parse(args) != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale workflows schedules --app <slug>", "workflows")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.ListWorkflowSchedules(context.Background(), *app)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	if response.UnavailableReason != "" {
		if _, err := fmt.Fprintf(osStdout, "Scheduling unavailable: %s\n", response.UnavailableReason); err != nil {
			return printErr("Output failed", err)
		}
	}
	writer := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "WORKFLOW\tSCHEDULE\tTIMEZONE\tENABLED\tCATCH UP\tWINDOW\tNEXT\tLAST STATUS\tLAST RUN"); err != nil {
		return printErr("Output failed", err)
	}
	for _, schedule := range response.Schedules {
		if schedule.CatchUp == "" {
			schedule.CatchUp = api.WorkflowScheduleCatchUpSkip
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%t\t%s\t%s\t%s\t%s\t%s\n", schedule.WorkflowName,
			schedule.Schedule, schedule.Timezone, schedule.Enabled, schedule.CatchUp, schedule.CatchUpWindow,
			schedule.NextFireAt, schedule.LastStatus, schedule.LastRunID); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}
