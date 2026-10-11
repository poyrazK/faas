package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"time"
)

func cmdEventsRetention(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events retention", flag.ContinueOnError)
	source := fs.String("source", "", "exact event source")
	app := fs.String("app", "", "application slug (receipt filter; storage remains account-wide)")
	window := fs.Duration("window", api.EventRetentionDefaultWindow, "expiry lookahead (1s..720h; default 24h)")
	limit := fs.Int("limit", api.EventRetentionSampleMax, "maximum sampled receipts (1..100)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events retention [--source SOURCE] [--app APP] [--window 24h] [--limit 100]", "events")
		return 1
	}
	q := api.EventRetentionQuery{Source: *source, App: *app, Window: *window, Limit: *limit}
	if *window <= 0 || *limit <= 0 {
		return printErr("Invalid retention query", fmt.Errorf("window and limit must be positive"))
	}
	if err := q.Validate(); err != nil {
		return printErr("Invalid retention query", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEventRetentionHealth(context.Background(), q)
	if err != nil {
		return printErr("Retention health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Retained: %d receipts / %d bytes | unsettled: %d | unknown deadlines: %d\nEligible for pruning: %d | expiring within %s: %d | held: %d (overdue: %d)\nAccount storage: count %.1f%% | bytes %.1f%% | maximum %.1f%%\n", out.RetainedReceipts, out.RetainedBytes, out.UnsettledReceipts, out.UnknownDeadlineReceipts, out.EligibleForPruning, time.Duration(out.WindowSeconds)*time.Second, out.ExpiringReceipts, out.HeldReceipts, out.HeldDueReceipts, out.StorageCountUtilizationPct, out.StorageBytesUtilizationPct, out.StorageUtilizationPct)
	_, _ = fmt.Fprintf(osStdout, "Holds: running backfill %d | retryable backfill %d | pending recovery %d\n", out.RunningBackfillHolds, out.RetryableBackfillHolds, out.RecoveryHolds)
	for _, sample := range out.Sample {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\n", sample.RetainUntil.Format(time.RFC3339), oneLine(sample.EventSource), oneLine(sample.EventID), sample.Status, sample.HoldReason)
	}
	if out.SampleTruncated {
		_, _ = fmt.Fprintln(osStdout, "Sample truncated; aggregate counts include all matching receipts.")
	}
	return 0
}
