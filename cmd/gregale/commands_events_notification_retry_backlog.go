package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsNotificationRetryBacklog(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events notification-retry-backlog", flag.ContinueOnError)
	status := fs.String("status", "failed,pending,inconclusive", "comma-separated request statuses")
	cursor := fs.String("cursor", "", "next job page cursor")
	size := fs.Int("page-size", api.EventRecoveryNotificationRetryBacklogJobsDefault, "jobs inspected per page (1..10; default 5)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events notification-retry-backlog <app> [--status STATUS,...] [--page-size N] [--cursor CURSOR]", "events")
		return 1
	}
	if *status == "" || *size < 1 {
		return printErr("Invalid retry backlog query", fmt.Errorf("status and page size must be nonempty and positive"))
	}
	query := api.EventRecoveryNotificationRetryBacklogQuery{Status: *status, Cursor: *cursor, PageSize: *size}
	if err := query.Normalize(); err != nil {
		return printErr("Invalid retry backlog query", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEventRecoveryNotificationRetryBacklog(context.Background(), positional[0], query)
	if err != nil {
		return printErr("Notification retry backlog failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "App %s | observed %s | jobs scanned %d | matched %d | totals scope: %s\n", oneLine(out.AppID), out.ObservedAt.Format(time.RFC3339), out.JobsScanned, out.MatchedCount, oneLine(out.CountsScope))
	_, _ = fmt.Fprintf(osStdout, "Page requests: %d | succeeded: %d | failed: %d | pending: %d | inconclusive: %d | incomplete evidence: %d\n", out.Totals.RequestCount, out.Totals.SucceededCount, out.Totals.FailedCount, out.Totals.PendingCount, out.Totals.InconclusiveCount, out.Totals.IncompleteEvidenceCount)
	_, _ = fmt.Fprintln(osStdout, "JOB\tREQUEST\tSTATUS\tSUCCEEDED\tFAILED\tPENDING\tUNKNOWN\tEVIDENCE COMPLETE\tDETAIL\tRETRY PREVIEW")
	for _, row := range out.Requests {
		s := row.Summary
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%t\t%s\t%s\n", oneLine(row.JobID), oneLine(s.RequestID), oneLine(s.Status), s.SucceededCount, s.FailedCount, s.PendingCount, s.UnknownCount, s.EvidenceComplete, oneLine(row.DetailPath), oneLine(row.RetryPreviewPath))
	}
	if out.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", oneLine(out.NextCursor))
	}
	return 0
}
