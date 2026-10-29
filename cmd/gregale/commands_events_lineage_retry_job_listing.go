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
	status := fs.String("status", "", "pending, paused, completed or cancelled; omit for all")
	attention := fs.String("attention", "", "any, stalled or retention_risk; active jobs only")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-retry-jobs APP SUB [--status STATE] [--limit N] [--after CURSOR] [--attention WARNING]", "events")
		return 1
	}
	option := api.EventExecutionLineageRetryJobAttentionOptions{Attention: *attention}
	if err := option.Validate(*status); err != nil {
		return printErr("Invalid attention filter", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEventExecutionLineageRetryJobs(context.Background(), positional[0], positional[1], *status, *after, *limit, option)
	if err != nil {
		return printErr("Lineage retry job listing failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Jobs) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No retained lineage retry jobs match.")
	}
	if out.Attention != nil {
		_, _ = fmt.Fprintf(osStdout, "Attention %s | observed %s | live page | has more %t\n", out.Attention.Filter, out.Attention.ObservedAt.Format(time.RFC3339Nano), out.Attention.HasMore)
	}
	for _, job := range out.Jobs {
		_, _ = fmt.Fprintf(osStdout, "Job %s | %s | processed %d/%d | created %s | expires %s\n", oneLine(job.RequestID), oneLine(job.Status), job.Processed, job.Selected, job.CreatedAt.Format(time.RFC3339Nano), job.RetainUntil.Format(time.RFC3339Nano))
		if job.Attention != nil {
			detail := job.Attention
			progress := "unknown"
			if detail.ProgressAgeSeconds != nil {
				progress = fmt.Sprintf("%.0fs", *detail.ProgressAgeSeconds)
			}
			_, _ = fmt.Fprintf(osStdout, "  Inactive %.0fs | progress age %s | remaining %d | stalled %t | retention risk %t\n", detail.InactivityAgeSeconds, progress, detail.Remaining, detail.StalledPending, detail.RetentionRisk)
		}
	}
	if out.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s (keep the same filters)\n", oneLine(out.NextAfter))
	}
	return 0
}
