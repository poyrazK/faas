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

func cmdEventsSubscriptionControl(args []string, action string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events subscription-"+action, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm changing subscription delivery")
	var command string
	var expectedMax, expectedPlan *int
	var capacity *int
	if action == "capacity" {
		fs.StringVar(&command, "command-id", "", "optional nonzero UUID; repeat unchanged after an uncertain response")
		expectedMax = fs.Int("expected-max-pending", -1, "reviewed saved capacity; requires expected-plan-max-pending")
		expectedPlan = fs.Int("expected-plan-max-pending", -1, "reviewed plan ceiling; requires expected-max-pending")
		capacity = fs.Int("max-pending", -1, "maximum pending plus dispatching deliveries; 0 restores plan limit")
	}
	var rate *int
	if action == "resume" {
		rate = fs.Int("rate", api.EventSubscriptionDrainRateDefault, "admissions per second (1..100; 0 removes pacing)")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || action != "status" && !*yes || rate != nil && (*rate < 0 || *rate > api.EventSubscriptionDrainRateMax) || capacity != nil && *capacity < 0 {
		usage := "usage: gregale events subscription-" + action + " <app> <subscription-id>"
		if action == "capacity" {
			usage += " --max-pending N"
		}
		if action == "resume" {
			usage += " [--rate N]"
		}
		if action != "status" {
			usage += " --yes"
		}
		PrintUsage(os.Stderr, usage, "events")
		return 1
	}
	var guard *api.EventSubscriptionCapacityGuard
	if action == "capacity" {
		provided := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "expected-max-pending" || f.Name == "expected-plan-max-pending" {
				provided = true
			}
		})
		if provided {
			guard = &api.EventSubscriptionCapacityGuard{MaxPendingDeliveries: expectedMax, PlanMaxPendingDeliveries: expectedPlan}
		}
		if err := (api.EventSubscriptionCapacityRequest{MaxPendingDeliveries: capacity, Expected: guard, CommandID: command}).Validate(); err != nil {
			return printErr("Invalid capacity request", err)
		}
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
	case "capacity":
		out, err = client.SetEventSubscriptionCapacity(context.Background(), positional[0], positional[1], api.EventSubscriptionCapacityRequest{MaxPendingDeliveries: capacity, Expected: guard, CommandID: command})
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
	if action == "capacity" && command != "" {
		if err := verifySubscriptionCapacityReceipt(out, positional[1], api.EventSubscriptionCapacityRequest{MaxPendingDeliveries: capacity, Expected: guard, CommandID: command}); err != nil {
			return printErr("Capacity command acknowledgment unconfirmed; repeat the same request", err)
		}
	}
	if action == "status" && out.CapacityCommandBudget != nil {
		if err := validateSubscriptionCapacityCommandBudget(out.CapacityCommandBudget, out.AppID, positional[1], time.Now().UTC(), api.EventDeliveryHealthWatchAgeDefault); err != nil {
			return printErr("Invalid capacity command receipt budget", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	mode := "running"
	if out.Paused {
		mode = "paused"
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s: %s | rate %d/s (0=unlimited) | pending %d | processing %d | oldest pending %.0fs\n", oneLine(out.SubscriptionID), mode, out.RatePerSecond, out.PendingRecipients, out.ProcessingRecipients, out.OldestAgeSeconds)
	_, _ = fmt.Fprintf(osStdout, "Live deliveries %d | effective subscription limit %d | configured %d (0=plan default)\n", out.LiveDeliveries, out.EffectiveMaxPendingDeliveries, out.MaxPendingDeliveries)
	if action == "status" {
		printSubscriptionCapacityCommandBudget(out.CapacityCommandBudget)
	}
	if receipt := out.CapacityCommandReceipt; receipt != nil {
		_, _ = fmt.Fprintf(osStdout, "Command %s | original %s setting %d (effective %d, plan %d) | retained until %s\n", receipt.CommandID, receipt.Outcome, receipt.RequestedMaxPendingDeliveries, receipt.EffectiveMaxPendingDeliveries, receipt.PlanMaxPendingDeliveries, receipt.RetainUntil.Format("2006-01-02T15:04:05Z07:00"))
	}
	if out.CircuitBreaker != nil {
		_, _ = fmt.Fprintf(osStdout, "Circuit %s | reason %s | recovery rate %d/s | history incomplete %t\n", oneLine(out.CircuitBreaker.State), oneLine(out.CircuitBreaker.Reason), out.CircuitBreaker.RecoveryRatePerSecond, out.CircuitBreaker.HistoryIncomplete)
	}
	return 0
}
