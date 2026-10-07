package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsBackfill(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events backfill", flag.ContinueOnError)
	subscription := fs.String("subscription-id", "", "target ordinary application subscription UUID")
	from := fs.String("from", "", "inclusive acceptance timestamp (RFC3339)")
	until := fs.String("until", "", "exclusive acceptance timestamp (RFC3339)")
	yes := fs.Bool("yes", false, "confirm that matching historical events may invoke this consumer")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	start, startErr := time.Parse(time.RFC3339Nano, *from)
	end, endErr := time.Parse(time.RFC3339Nano, *until)
	subID, subErr := uuid.Parse(*subscription)
	req := api.EventReplayBackfillRequest{From: start, Until: end}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || !*yes || startErr != nil || endErr != nil || subErr != nil || req.Validate() != nil {
		PrintUsage(os.Stderr, "usage: gregale events backfill <app> --subscription-id UUID --from RFC3339 --until RFC3339 --yes", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	job, err := client.CreateEventReplayBackfill(context.Background(), positional[0], subID.String(), req)
	if err != nil {
		return printErr("Event backfill creation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(job))
	}
	writeEventBackfillJob(job)
	return 0
}

func cmdEventsBackfillStatus(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events backfill-status", flag.ContinueOnError)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events backfill-status <job-id>", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		PrintUsage(os.Stderr, "usage: gregale events backfill-status <job-id>", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	job, err := client.GetEventReplayBackfill(context.Background(), positional[0])
	if err != nil {
		return printErr("Event backfill status failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(job))
	}
	writeEventBackfillJob(job)
	return 0
}

func cmdEventsBackfillItems(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events backfill-items", flag.ContinueOnError)
	stateFilter := fs.String("state", "", "filter by a routing outcome state")
	after := fs.String("after", "", "opaque continuation cursor from the previous page")
	limit := fs.Int("limit", api.EventReplayBackfillItemsPageDefault, "items per page (1..100; default 50)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || validateCLILimit("limit", *limit, api.EventReplayBackfillItemsPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events backfill-items <job-id> [--state STATE] [--limit N] [--after CURSOR]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		PrintUsage(os.Stderr, "usage: gregale events backfill-items <job-id> [--state STATE] [--limit N] [--after CURSOR]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListEventReplayBackfillItems(context.Background(), positional[0], *stateFilter, *after, *limit)
	if err != nil {
		return printErr("Event backfill item read failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No backfill items on this page for job %s.\n", page.JobID)
	}
	for _, item := range page.Items {
		_, _ = fmt.Fprintf(osStdout, "%s %s %s %s attempts=%d retryable=%t", item.AcceptedAt.Format(time.RFC3339Nano), oneLine(item.EventSource), oneLine(item.EventID), item.State, item.Attempts, item.Retryable)
		if item.FailureCode != "" {
			_, _ = fmt.Fprintf(osStdout, " failure=%s", oneLine(item.FailureCode))
		}
		if item.LastError != "" {
			_, _ = fmt.Fprintf(osStdout, " error=%s", oneLine(item.LastError))
			if item.DetailsTruncated {
				_, _ = fmt.Fprint(osStdout, " [truncated]")
			}
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if page.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: gregale events backfill-items %s --limit %d", page.JobID, *limit)
		if *stateFilter != "" {
			_, _ = fmt.Fprintf(osStdout, " --state %s", *stateFilter)
		}
		_, _ = fmt.Fprintf(osStdout, " --after %s\n", page.NextAfter)
	}
	return 0
}

func cmdEventsBackfillRetry(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events backfill-retry", flag.ContinueOnError)
	limit := fs.Int("limit", api.EventReplayBackfillRetryMax, "failed routing recipients to requeue (1..100)")
	yes := fs.Bool("yes", false, "confirm requeueing failed event deliveries")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || !*yes || validateCLILimit("limit", *limit, api.EventReplayBackfillRetryMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events backfill-retry <job-id> [--limit N] --yes", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		PrintUsage(os.Stderr, "usage: gregale events backfill-retry <job-id> [--limit N] --yes", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.RetryFailedEventReplayBackfill(context.Background(), positional[0], *limit)
	if err != nil {
		return printErr("Event backfill retry failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Requeued %d failed routing deliveries; %d remain retryable.\n", result.RetriedCount, result.RemainingRetryableCount)
	writeEventBackfillJob(result.Job)
	return 0
}

func writeEventBackfillJob(job api.EventReplayBackfillJobResponse) {
	_, _ = fmt.Fprintf(osStdout, "Backfill %s for %s / %s: %s\n", job.ID, oneLine(job.AppSlug), job.SubscriptionID, job.State)
	_, _ = fmt.Fprintf(osStdout, "Acceptance window: [%s, %s) | cutoff: %s | scan complete: %t\n", job.From.Format(time.RFC3339Nano), job.Until.Format(time.RFC3339Nano), job.CutoffAt.Format(time.RFC3339Nano), job.ScanComplete)
	_, _ = fmt.Fprintf(osStdout, "Scanned %d | matched %d | filtered %d | pending %d | processing %d | enqueued %d | failed %d\n", job.Progress.Scanned, job.Progress.Matched, job.Progress.Filtered, job.Progress.Pending, job.Progress.Processing, job.Progress.Enqueued, job.Progress.Failed)
	_, _ = fmt.Fprintf(osStdout, "Retryable failed: %d\n", job.Progress.RetryableFailed)
	_, _ = fmt.Fprintf(osStdout, "Skipped: captured %d | unknown membership %d | existing target %d | unsettled receipt %d\n", job.Progress.SkippedCaptured, job.Progress.SkippedUnknown, job.Progress.SkippedExisting, job.Progress.SkippedUnsettled)
	_, _ = fmt.Fprintln(osStdout, "Routing enqueued handler invocations; it does not mean the handler completed. Retained history is not a complete archive.")
}
