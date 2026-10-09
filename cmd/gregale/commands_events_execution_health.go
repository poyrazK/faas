package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
)

func cmdEventsSubscriptionExecutionHealth(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscription-execution-health", flag.ContinueOnError)
	window := fs.String("window", "5m", "observation window: 5m|15m|1h|6h|24h")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscription-execution-health <app> <subscription-id> [--window 15m]", "events")
		return 1
	}
	if _, err := api.EventConsumerHealthWindow(*window); err != nil {
		return printErr("Invalid window", err)
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	h, err := client.GetEventConsumerExecutionHealth(context.Background(), positional[0], positional[1], *window)
	if err != nil {
		return printErr("Execution health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(h))
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | executions %d | queued %d | running %d | retrying %d\n", oneLine(h.SubscriptionID), h.Executions, h.Queued, h.Running, h.Retrying)
	_, _ = fmt.Fprintf(osStdout, "Succeeded %d | failed %d | expired %d | dead letters %d | cancelled %d | superseded %d | unknown %d\n", h.Succeeded, h.Failed, h.Expired, h.DeadLettered, h.Cancelled, h.Superseded, h.Unknown)
	_, _ = fmt.Fprintf(osStdout, "Window %s | successful attempts %d | failed attempts %d (%.2f%%) | unknown attempts %d | dead-letter outcomes %d (%.3f/s) | completions %d | completion p95 %.3fs\n", *window, h.SuccessfulAttempts, h.FailedAttempts, h.HandlerFailurePct, h.UnknownAttempts, h.WindowDeadLetters, h.DeadLetterRatePerSecond, h.WindowCompletions, h.CompletionLatencyP95Seconds)
	_, _ = fmt.Fprintf(osStdout, "Coverage %s | retained roots %d | missing roots %d | truncated %t | history complete %t\n", oneLine(h.Coverage), h.RetainedRoots, h.MissingRoots, h.Truncated, h.HistoryComplete)
	return 0
}
