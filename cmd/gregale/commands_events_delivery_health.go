package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionDeliveryHealth(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscription-delivery-health", flag.ContinueOnError)
	window := fs.String("window", "5m", "observation window: 5m|15m|1h|6h|24h")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscription-delivery-health <app> <subscription-id> [--window 5m]", "events")
		return 1
	}
	if _, err := api.EventConsumerHealthWindow(*window); err != nil {
		return printErr("Invalid window", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEventConsumerDeliveryHealth(context.Background(), positional[0], positional[1], *window)
	if err != nil {
		return printErr("Consumer delivery health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printEventConsumerDeliveryHealth(out)
	return 0
}

func printEventConsumerDeliveryHealth(out api.EventConsumerDeliveryHealth) {
	r, e, sample := out.Routing, out.Execution, out.PendingSample
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | pending routing %d | processing routing %d | oldest pending %.0fs\n", oneLine(r.SubscriptionID), r.PendingRecipients, r.ProcessingRecipients, r.OldestAgeSeconds)
	_, _ = fmt.Fprintf(osStdout, "Retained executions: queued %d | running %d | retrying %d | dead letters %d | truncated %t | history complete %t\n", e.Queued, e.Running, e.Retrying, e.DeadLettered, e.Truncated, e.HistoryComplete)
	if e.LatestSuccessfulCompletionAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Latest retained successful completion: %s\n", e.LatestSuccessfulCompletionAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	} else {
		_, _ = fmt.Fprintln(osStdout, "Latest retained successful completion: unavailable")
	}
	_, _ = fmt.Fprintf(osStdout, "Controls: paused %t | configured rate %d/s\n", r.Paused, r.RatePerSecond)
	if r.CircuitBreaker != nil {
		_, _ = fmt.Fprintf(osStdout, "Circuit: %s | reason %s\n", oneLine(r.CircuitBreaker.State), oneLine(r.CircuitBreaker.Reason))
	}
	_, _ = fmt.Fprintf(osStdout, "Oldest pending sample: %d | truncated %t | waiting reasons %v\n", len(sample.Recipients), sample.Truncated, sample.WaitingReasons)
	if len(sample.Recipients) > 0 {
		first := sample.Recipients[0]
		_, _ = fmt.Fprintf(osStdout, "Oldest sampled event: source %s | ID %s | accepted %s | blocker %s\n", oneLine(first.EventSource), oneLine(first.EventID), first.AcceptedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), oneLine(first.WaitingReason))
	}
	_, _ = fmt.Fprintf(osStdout, "Independent reads: routing %s | execution %s | pending %s | routing history compacted %t\n", r.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), e.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), sample.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), r.HistoryCompacted)
}
