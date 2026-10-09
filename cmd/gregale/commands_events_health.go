package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionHealth(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscription-health", flag.ContinueOnError)
	window := fs.String("window", "5m", "observation window: 5m|15m|1h|6h|24h")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscription-health <app> <subscription-id> [--window 5m]", "events")
		return 1
	}
	if _, err := api.EventConsumerHealthWindow(*window); err != nil {
		return printErr("Invalid window", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEventConsumerHealth(context.Background(), positional[0], positional[1], *window)
	if err != nil {
		return printErr("Consumer health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | pending %d | processing %d | oldest %.0fs | paused %t (%.0fs)\n", oneLine(out.SubscriptionID), out.PendingRecipients, out.ProcessingRecipients, out.OldestAgeSeconds, out.Paused, out.PausedSeconds)
	_, _ = fmt.Fprintf(osStdout, "Window %s | routes %d | failures %d (%.2f%%) | retries scheduled %.3f/s | drain %.3f/s | routing p95 %.3fs | history compacted %t\n", *window, out.SuccessfulRoutes, out.TerminalFailures, out.TerminalFailurePct, out.RetryRatePerSecond, out.DrainRatePerSecond, out.RoutingLatencyP95Seconds, out.HistoryCompacted)
	_, _ = fmt.Fprintf(osStdout, "Expired deliveries in window: %d\n", out.ExpiredDeliveries)
	if out.CircuitBreaker != nil {
		_, _ = fmt.Fprintf(osStdout, "Circuit %s | reason %s | recovery rate %d/s | history incomplete %t\n", oneLine(out.CircuitBreaker.State), oneLine(out.CircuitBreaker.Reason), out.CircuitBreaker.RecoveryRatePerSecond, out.CircuitBreaker.HistoryIncomplete)
	}
	return 0
}
