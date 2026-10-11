package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsWorkflowBackfill(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events workflow-backfill", flag.ContinueOnError)
	workflow := fs.String("workflow-name", "", "target event-triggered workflow name")
	from := fs.String("from", "", "inclusive acceptance timestamp (RFC3339)")
	until := fs.String("until", "", "exclusive acceptance timestamp (RFC3339)")
	yes := fs.Bool("yes", false, "confirm that matching historical events may start workflow runs")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	start, startErr := time.Parse(time.RFC3339Nano, *from)
	end, endErr := time.Parse(time.RFC3339Nano, *until)
	req := api.WorkflowEventReplayBackfillRequest{WorkflowName: *workflow, From: start, Until: end}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || !*yes || startErr != nil || endErr != nil || req.Validate() != nil {
		PrintUsage(os.Stderr, "usage: gregale events workflow-backfill <app> --workflow-name NAME --from RFC3339 --until RFC3339 --yes", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	job, err := client.CreateWorkflowEventReplayBackfill(context.Background(), positional[0], req)
	if err != nil {
		return printErr("Workflow event backfill creation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(job))
	}
	writeEventBackfillJob(job)
	return 0
}
