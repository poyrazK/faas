package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdCronsCancel requests cancellation of one command-cron run. The cron and
// task ids are checked locally so malformed input does not make an API call.
func cmdCronsCancel(args []string) int {
	fs := newFlagSet("crons-cancel", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: gregale crons cancel <cron-id> <run-id>")
		return 1
	}
	cronID, runID := fs.Arg(0), fs.Arg(1)
	if !cronIDPattern.MatchString(cronID) || !fireNowRequestIDPattern.MatchString(runID) {
		fmt.Fprintln(os.Stderr, "usage: gregale crons cancel <cron-id> <run-id>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	task, err := client.CancelCronCommandRun(context.Background(), cronID, runID)
	if err != nil {
		return printErr("Could not cancel cron run", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(task))
	}
	switch {
	case task.Status == api.AppTaskStatusCancelled:
		_, _ = fmt.Fprintf(osStdout, "Cron run %s cancelled.\n", task.ID)
	case task.CancelRequestedAt != nil:
		_, _ = fmt.Fprintf(osStdout, "Cancellation requested for cron run %s (status=%s).\n", task.ID, task.Status)
	case task.Status.Terminal():
		_, _ = fmt.Fprintf(osStdout, "Cron run %s is already terminal (status=%s).\n", task.ID, task.Status)
	default:
		_, _ = fmt.Fprintf(osStdout, "Cancellation accepted for cron run %s (status=%s).\n", task.ID, task.Status)
	}
	return 0
}
