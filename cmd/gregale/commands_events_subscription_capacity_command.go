package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionCapacityCommand(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscription-capacity-command", flag.ContinueOnError)
	timeout := fs.Duration("timeout", api.EventSubscriptionControlTimeout, "total read deadline")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || rejectUnexpectedFlagArgs(fs) || *timeout <= 0 || *timeout > time.Minute {
		PrintUsage(os.Stderr, "usage: gregale events subscription-capacity-command <app> <subscription-id> <command-id> [--timeout DURATION]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	zero := 0
	if positional[2] == "" {
		return 1
	}
	if err := (api.EventSubscriptionCapacityRequest{CommandID: positional[2], MaxPendingDeliveries: &zero}).Validate(); err != nil {
		return printErr("Invalid command ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interrupted, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, *timeout)
	defer cancel()
	app, err := client.GetApp(ctx, positional[0])
	if interrupted.Err() != nil {
		return 130
	}
	if err != nil {
		return printErr("Capacity command lookup failed", err)
	}
	receipt, err := client.GetEventSubscriptionCapacityCommand(ctx, positional[0], positional[1], positional[2])
	if interrupted.Err() != nil {
		return 130
	}
	if err != nil {
		return printErr("Capacity command evidence unavailable; absence does not prove it never ran", err)
	}
	req := api.EventSubscriptionCapacityRequest{CommandID: positional[2], MaxPendingDeliveries: &receipt.RequestedMaxPendingDeliveries, Expected: receipt.Expected}
	out := api.EventSubscriptionDeliveryControl{AppID: app.ID, SubscriptionID: positional[1], CapacityCommandReceipt: &receipt}
	if err := req.Validate(); err != nil {
		return printErr("Invalid capacity command evidence", err)
	}
	if err := verifySubscriptionCapacityReceipt(out, positional[1], req); err != nil {
		return printErr("Invalid capacity command evidence", err)
	}
	if receipt.Expected != nil && (*receipt.Expected.MaxPendingDeliveries != receipt.PreviousMaxPendingDeliveries || *receipt.Expected.PlanMaxPendingDeliveries != receipt.PlanMaxPendingDeliveries) {
		return printErr("Invalid capacity command evidence", fmt.Errorf("original expectations disagree with receipt"))
	}
	now := time.Now().UTC()
	if receipt.RecordedAt.After(now) || !receipt.RetainUntil.After(now) {
		return printErr("Capacity command evidence is no longer retained", fmt.Errorf("refresh the read-only lookup"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "Command %s: original %s setting %d (previous %d, effective %d, plan %d)\nRecorded %s | retained until %s\n", receipt.CommandID, receipt.Outcome, receipt.RequestedMaxPendingDeliveries, receipt.PreviousMaxPendingDeliveries, receipt.EffectiveMaxPendingDeliveries, receipt.PlanMaxPendingDeliveries, receipt.RecordedAt.Format(time.RFC3339), receipt.RetainUntil.Format(time.RFC3339))
	return 0
}
