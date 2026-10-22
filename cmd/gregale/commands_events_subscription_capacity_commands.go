package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionCapacityCommands(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscription-capacity-commands", flag.ContinueOnError)
	outcome := fs.String("outcome", "any", "any, applied or unchanged")
	after := fs.String("after", "", "opaque next_after from the same scope and outcome filter")
	limit := fs.Int("limit", api.EventSubscriptionCapacityCommandPageDefault, "receipts per page (maximum 100)")
	timeout := fs.Duration("timeout", api.EventSubscriptionControlTimeout, "total read deadline")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || *limit < 1 || *limit > api.EventSubscriptionCapacityCommandPageMax || len(*after) > api.EventSubscriptionCapacityCommandCursorMaxBytes || (*outcome != "any" && *outcome != "applied" && *outcome != "unchanged") || *timeout <= 0 || *timeout > time.Minute {
		PrintUsage(os.Stderr, "usage: gregale events subscription-capacity-commands <app> <subscription-id> [--outcome any|applied|unchanged] [--limit N] [--after CURSOR] [--timeout DURATION]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
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
		return printErr("Capacity command history failed", err)
	}
	query := api.EventSubscriptionCapacityCommandsQuery{Outcome: *outcome, After: *after, Limit: *limit}
	out, err := client.ListEventSubscriptionCapacityCommands(ctx, positional[0], positional[1], query)
	if interrupted.Err() != nil {
		return 130
	}
	if err != nil {
		return printErr("Capacity command history failed", err)
	}
	if err = verifySubscriptionCapacityCommands(out, app.ID, positional[1], query); err != nil {
		return printErr("Invalid capacity command history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | retained command page (%s) | observed %s\n", out.SubscriptionID, out.Outcome, out.ObservedAt.Format(time.RFC3339))
	for _, r := range out.Receipts {
		_, _ = fmt.Fprintf(osStdout, "%s | %s | original setting %d (previous %d, effective %d, plan %d) | retained until %s\n", r.CommandID, r.Outcome, r.RequestedMaxPendingDeliveries, r.PreviousMaxPendingDeliveries, r.EffectiveMaxPendingDeliveries, r.PlanMaxPendingDeliveries, r.RetainUntil.Format(time.RFC3339))
	}
	if len(out.Receipts) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No matching retained receipts; absence does not prove a command never ran.")
	}
	if out.HasMore {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s (keep the same app, subscription and outcome filter)\n", out.NextAfter)
	}
	return 0
}

func verifySubscriptionCapacityCommands(out api.EventSubscriptionCapacityCommands, app, sub string, query api.EventSubscriptionCapacityCommandsQuery) error {
	if !sameBindingDeployment(out.AppID, app) || !sameBindingDeployment(out.SubscriptionID, sub) || out.Outcome != query.Outcome || out.ObservedAt.IsZero() || out.Receipts == nil || len(out.Receipts) > query.Limit || out.HasMore != (out.NextAfter != "") || len(out.NextAfter) > api.EventSubscriptionCapacityCommandCursorMaxBytes || (out.HasMore && (len(out.Receipts) != query.Limit || out.NextAfter == query.After)) {
		return fmt.Errorf("invalid scope, filter or page metadata")
	}
	if out.NextAfter != "" {
		raw, err := base64.RawURLEncoding.DecodeString(out.NextAfter)
		if err != nil || base64.RawURLEncoding.EncodeToString(raw) != out.NextAfter {
			return fmt.Errorf("invalid continuation encoding")
		}
	}
	seen := map[string]bool{}
	for i, r := range out.Receipts {
		req := api.EventSubscriptionCapacityRequest{CommandID: r.CommandID, MaxPendingDeliveries: &r.RequestedMaxPendingDeliveries, Expected: r.Expected}
		if r.CommandID == "" || req.Validate() != nil || r.RecordedAt.After(out.ObservedAt) || !r.RetainUntil.After(out.ObservedAt) || (query.Outcome != "any" && r.Outcome != query.Outcome) {
			return fmt.Errorf("invalid retained command at index %d", i)
		}
		control := api.EventSubscriptionDeliveryControl{AppID: app, SubscriptionID: sub, CapacityCommandReceipt: &r}
		if err := verifySubscriptionCapacityReceipt(control, sub, req); err != nil {
			return err
		}
		if r.Expected != nil && (*r.Expected.MaxPendingDeliveries != r.PreviousMaxPendingDeliveries || *r.Expected.PlanMaxPendingDeliveries != r.PlanMaxPendingDeliveries) {
			return fmt.Errorf("inconsistent original expectations")
		}
		id := strings.ToLower(r.CommandID)
		if seen[id] {
			return fmt.Errorf("duplicate command receipt")
		}
		seen[id] = true
		if i > 0 {
			prior := out.Receipts[i-1]
			if r.RecordedAt.After(prior.RecordedAt) || (r.RecordedAt.Equal(prior.RecordedAt) && id >= strings.ToLower(prior.CommandID)) {
				return fmt.Errorf("invalid command history order")
			}
		}
	}
	return nil
}
