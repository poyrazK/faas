package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionControl(args []string, action string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events subscription-"+action, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm changing subscription delivery")
	var rate *int
	if action == "resume" {
		rate = fs.Int("rate", api.EventSubscriptionDrainRateDefault, "admissions per second (1..100; 0 removes pacing)")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || action != "status" && !*yes || rate != nil && (*rate < 0 || *rate > api.EventSubscriptionDrainRateMax) {
		usage := "usage: gregale events subscription-" + action + " <app> <subscription-id>"
		if action == "resume" {
			usage += " [--rate N]"
		}
		if action != "status" {
			usage += " --yes"
		}
		PrintUsage(os.Stderr, usage, "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventSubscriptionDeliveryControl
	switch action {
	case "pause":
		out, err = client.PauseEventSubscription(context.Background(), positional[0], positional[1])
	case "resume":
		out, err = client.ResumeEventSubscription(context.Background(), positional[0], positional[1], api.EventSubscriptionResumeRequest{RatePerSecond: rate})
	default:
		out, err = client.GetEventSubscriptionDeliveryControl(context.Background(), positional[0], positional[1])
	}
	if err != nil {
		return printErr("Subscription delivery control failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	mode := "running"
	if out.Paused {
		mode = "paused"
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s: %s | rate %d/s (0=unlimited) | pending %d | processing %d | oldest pending %.0fs\n", oneLine(out.SubscriptionID), mode, out.RatePerSecond, out.PendingRecipients, out.ProcessingRecipients, out.OldestAgeSeconds)
	if out.CircuitBreaker != nil {
		_, _ = fmt.Fprintf(osStdout, "Circuit %s | reason %s | recovery rate %d/s | history incomplete %t\n", oneLine(out.CircuitBreaker.State), oneLine(out.CircuitBreaker.Reason), out.CircuitBreaker.RecoveryRatePerSecond, out.CircuitBreaker.HistoryIncomplete)
	}
	return 0
}
