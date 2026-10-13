package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageRetryJobs(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-jobs", flag.ContinueOnError)
	limit := fs.Int("limit", api.EventExecutionLineageRetryJobsPageDefault, "page size (maximum 200)")
	after := fs.String("after", "", "continuation cursor from the previous page")
	status := fs.String("status", "", "pending, completed or cancelled; omit for all")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscription-lineage-retry-jobs APP SUB [--status STATE] [--limit N] [--after CURSOR]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEventExecutionLineageRetryJobs(context.Background(), positional[0], positional[1], *status, *after, *limit)
	if err != nil {
		return printErr("Lineage retry job listing failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Jobs) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No retained lineage retry jobs match.")
	}
	for _, job := range out.Jobs {
		_, _ = fmt.Fprintf(osStdout, "Job %s | %s | processed %d/%d | created %s | expires %s\n", oneLine(job.RequestID), oneLine(job.Status), job.Processed, job.Selected, job.CreatedAt.Format(time.RFC3339Nano), job.RetainUntil.Format(time.RFC3339Nano))
	}
	if out.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s\n", oneLine(out.NextAfter))
	}
	return 0
}
