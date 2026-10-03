package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const eventFanoutReplayBatchMax = 100

// cmdEvents exposes the customer-facing internal event fabric. Preview
// simulates account-wide routing; publishing is the producer path, while
// subscriptions and deliveries inspect declarations and delivery outcomes.
func cmdEvents(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale events <preview|publish|subscriptions|deliveries|fanout-history|replay|replay-retryable>", "events")
		return 1
	}
	switch args[0] {
	case "preview":
		return cmdEventsPreview(args[1:])
	case "publish":
		return cmdEventsPublish(args[1:])
	case "subscriptions", "list":
		return cmdEventsSubscriptions(args[1:])
	case "deliveries":
		return cmdEventsDeliveries(args[1:])
	case "fanout-history":
		return cmdEventsFanoutHistory(args[1:])
	case "replay":
		return cmdEventsReplayFanoutFailure(args[1:])
	case "replay-retryable":
		return cmdEventsReplayRetryableFanoutFailures(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown events subcommand: %s", args[0]), "events")
		return 1
	}
}

// cmdEventsReplayRetryableFanoutFailures retries a bounded set of terminal
// pre-invocation recipients that were classified as retryable.
func cmdEventsReplayRetryableFanoutFailures(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events replay-retryable", flag.ContinueOnError)
	eventSource := fs.String("event-source", "", "limit replay to one published event source")
	eventID := fs.String("event-id", "", "limit replay to one published event")
	limit := fs.Int("limit", eventFanoutReplayBatchMax, "max recipients to requeue (1..100)")
	yes := fs.Bool("yes", false, "confirm requeueing retryable event recipients")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || !*yes ||
		(strings.TrimSpace(*eventSource) == "") != (strings.TrimSpace(*eventID) == "") ||
		validateCLILimit("limit", *limit, eventFanoutReplayBatchMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events replay-retryable <app> [--event-source SOURCE --event-id ID] [--limit N] --yes", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ReplayRetryableEventFanoutFailures(context.Background(), positional[0], api.ReplayRetryableEventFanoutFailuresRequest{
		EventSource: strings.TrimSpace(*eventSource), EventID: strings.TrimSpace(*eventID), Limit: *limit,
	})
	if err != nil {
		return printErr("Retryable event fanout replay failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Queued %d retryable event recipient(s) for %s.", resp.ReplayedCount, resp.AppSlug)
	if resp.HasMore {
		_, _ = fmt.Fprintln(osStdout, "More retryable failures remain; run the command again to queue the next bounded batch.")
	}
	return 0
}

// cmdEventsReplayFanoutFailure retries one terminal pre-invocation recipient.
func cmdEventsReplayFanoutFailure(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events replay", flag.ContinueOnError)
	eventID := fs.String("event-id", "", "published event id from events deliveries")
	eventSource := fs.String("event-source", "", "published event source from events deliveries")
	subscriptionID := fs.String("subscription-id", "", "failed subscription id from events deliveries")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*eventID) == "" ||
		strings.TrimSpace(*eventSource) == "" || strings.TrimSpace(*subscriptionID) == "" {
		PrintUsage(os.Stderr, "usage: gregale events replay <app> --event-id ID --event-source SOURCE --subscription-id ID", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ReplayEventFanoutFailure(context.Background(), positional[0], api.ReplayEventFanoutFailureRequest{
		EventID: strings.TrimSpace(*eventID), EventSource: strings.TrimSpace(*eventSource),
		SubscriptionID: strings.TrimSpace(*subscriptionID),
	})
	if err != nil {
		return printErr("Event fanout replay failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Event %s from %s queued for recipient %s.", resp.EventID, resp.EventSource, resp.SubscriptionID)
	return 0
}

// cmdEventsPreview evaluates an event against enabled account subscriptions
// without creating an event row or enqueueing any invocations.
func cmdEventsPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events preview", flag.ContinueOnError)
	id := fs.String("id", "", "event id to use when filters inspect the CloudEvents id")
	source := fs.String("source", "", "event source (or the first positional argument)")
	typ := fs.String("type", "", "event type (or the second positional argument)")
	data := fs.String("data", "", "JSON event data (inline | @file | - for stdin; required)")
	occurredAt := fs.String("time", "", "event time (RFC3339; defaults to server time)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) > 2 {
		PrintUsage(os.Stderr, "usage: gregale events preview [SOURCE TYPE] --data <json|@file|-> [--id ID] [--time RFC3339]", "events")
		return 1
	}
	if len(positional) >= 1 {
		if *source != "" {
			return printErr("Invalid arguments", fmt.Errorf("source provided both positionally and with --source"))
		}
		*source = positional[0]
	}
	if len(positional) == 2 {
		if *typ != "" {
			return printErr("Invalid arguments", fmt.Errorf("type provided both positionally and with --type"))
		}
		*typ = positional[1]
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*source) == "" || strings.TrimSpace(*typ) == "" || strings.TrimSpace(*data) == "" {
		PrintUsage(os.Stderr, "usage: gregale events preview [SOURCE TYPE] --data <json|@file|-> [--id ID] [--time RFC3339]", "events")
		return 1
	}
	body, err := resolvePayload(*data)
	if err != nil {
		return printErr("Invalid --data", err)
	}
	if len(body) == 0 || !json.Valid(body) {
		return printErr("Invalid --data", fmt.Errorf("must be a non-empty JSON value"))
	}
	var eventTime *time.Time
	if strings.TrimSpace(*occurredAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *occurredAt)
		if parseErr != nil {
			return printErr("Invalid --time", fmt.Errorf("must be RFC3339: %w", parseErr))
		}
		eventTime = &parsed
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PreviewEvent(context.Background(), api.PreviewEventRequest{
		ID:     strings.TrimSpace(*id),
		Source: strings.TrimSpace(*source),
		Type:   strings.TrimSpace(*typ),
		Time:   eventTime,
		Data:   json.RawMessage(body),
	})
	if err != nil {
		return printErr("Event preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "Event preview %s %s (id %s)\n", resp.Source, resp.Type, resp.EventID)
	_, _ = fmt.Fprintf(osStdout, "Would deliver: %d  |  Filtered: %d  |  Other mismatches: %d\n", resp.MatchedCount, resp.FilterMismatchCount, resp.OtherMismatchCount)
	if resp.CandidateCount == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no enabled subscriptions match this source and type)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "APP\tSUBSCRIPTION\tWORKFLOW\tRESULT\tFILTER")
	for _, subscription := range resp.Matches {
		writeEventPreviewSubscription(subscription)
	}
	for _, subscription := range resp.NonMatches {
		writeEventPreviewSubscription(subscription)
	}
	if resp.Truncated {
		_, _ = fmt.Fprintln(osStdout, "... showing a bounded sample; use --json for counts and sample details")
	}
	return 0
}

func writeEventPreviewSubscription(subscription api.EventPreviewSubscription) {
	filter := string(subscription.Filter)
	if filter == "" {
		filter = "{}"
	}
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\n", subscription.AppSlug, subscription.SubscriptionID, subscription.WorkflowName, subscription.Reason, filter)
}

// cmdEventsDeliveries implements `gregale events deliveries <app>`. It is a
// focused operational view for event-triggered invocations and routing
// failures that happen before invocation creation.
func cmdEventsDeliveries(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events deliveries", flag.ContinueOnError)
	eventSource := fs.String("event-source", "", "narrow event filter to one published source; requires --event-id")
	eventID := fs.String("event-id", "", "filter by published event id")
	deliveryState := fs.String("state", "", "filter by delivery state; failed includes recipient fanout failures")
	before := fs.String("before", "", "pagination cursor (NextBefore from a prior call)")
	fanoutBefore := fs.String("fanout-before", "", "pagination cursor (NextFanoutBefore from a prior call)")
	limit := fs.Int("limit", 20, "max deliveries (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) ||
		(strings.TrimSpace(*eventSource) != "" && strings.TrimSpace(*eventID) == "") ||
		validateCLILimit("limit", *limit, 200) != nil {
		PrintUsage(os.Stderr, "usage: gregale events deliveries <app> [--event-source SOURCE --event-id ID] [--state STATE] [--before CURSOR] [--fanout-before CURSOR] [--limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListEventDeliveriesPageByEventIdentity(context.Background(), positional[0],
		strings.TrimSpace(*eventSource), strings.TrimSpace(*eventID), *deliveryState, *before, *fanoutBefore, *limit)
	if err != nil {
		return printErr("Could not list event deliveries", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Deliveries) == 0 && len(resp.FanoutFailures) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no event deliveries)")
		return 0
	}
	if len(resp.Deliveries) > 0 {
		_, _ = fmt.Fprintln(osStdout, "INVOCATION\tINVOCATION_SOURCE\tEVENT\tSOURCE\tTYPE\tSTATE\tATTEMPTS\tCREATED\tERROR")
		for _, delivery := range resp.Deliveries {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
				delivery.InvocationID,
				delivery.InvocationSource,
				delivery.EventID,
				delivery.EventSource,
				delivery.EventType,
				delivery.State,
				delivery.Attempts,
				delivery.CreatedAt.Format(time.RFC3339),
				oneLine(delivery.LastError),
			)
		}
		if resp.NextBefore != "" {
			_, _ = fmt.Fprintf(osStdout, "... more invocations — pass --before %s\n", resp.NextBefore)
		}
	}
	if len(resp.FanoutFailures) > 0 {
		_, _ = fmt.Fprintln(osStdout, "PRE-INVOCATION FANOUT FAILURES")
		_, _ = fmt.Fprintln(osStdout, "EVENT\tSOURCE\tTYPE\tSUBSCRIPTION\tSTATE\tATTEMPTS\tFAILURE_CODE\tRETRYABLE\tFAILED\tERROR")
		for _, failure := range resp.FanoutFailures {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%t\t%s\t%s\n",
				oneLine(failure.EventID), oneLine(failure.EventSource), oneLine(failure.EventType), oneLine(failure.SubscriptionID),
				oneLine(failure.State), failure.Attempts, oneLine(failure.FailureCode), failure.Retryable,
				failure.FailedAt.Format(time.RFC3339), oneLine(failure.LastError),
			)
		}
	}
	if resp.NextFanoutBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more fanout failures — pass --fanout-before %s\n", resp.NextFanoutBefore)
	}
	return 0
}

// cmdEventsFanoutHistory shows immutable pre-invocation routing outcomes and
// explicit replay requests for one event recipient set.
func cmdEventsFanoutHistory(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events fanout-history", flag.ContinueOnError)
	eventSource := fs.String("event-source", "", "published event source")
	eventID := fs.String("event-id", "", "published event id")
	subscriptionID := fs.String("subscription-id", "", "narrow history to one recipient")
	before := fs.String("before", "", "pagination cursor (NextBefore from a prior call)")
	limit := fs.Int("limit", 20, "max history rows (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) ||
		strings.TrimSpace(*eventSource) == "" || strings.TrimSpace(*eventID) == "" ||
		validateCLILimit("limit", *limit, 200) != nil {
		PrintUsage(os.Stderr, "usage: gregale events fanout-history <app> --event-source SOURCE --event-id ID [--subscription-id ID] [--before CURSOR] [--limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListEventFanoutAttemptHistory(context.Background(), positional[0],
		strings.TrimSpace(*eventSource), strings.TrimSpace(*eventID), strings.TrimSpace(*subscriptionID), *before, *limit)
	if err != nil {
		return printErr("Could not list event fanout history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.History) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no event fanout history)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "EVENT\tSOURCE\tSUBSCRIPTION\tACTION\tSTATE\tATTEMPT\tFAILURE_CODE\tRETRYABLE\tRECORDED\tERROR")
	for _, row := range resp.History {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%t\t%s\t%s\n",
			oneLine(resp.EventID), oneLine(resp.EventSource), oneLine(row.SubscriptionID),
			oneLine(row.Action), oneLine(row.State), row.AttemptNumber, oneLine(row.FailureCode),
			row.Retryable, row.OccurredAt.Format(time.RFC3339), oneLine(row.LastError))
	}
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more history — pass --before %s\n", resp.NextBefore)
	}
	return 0
}

// cmdEventsSubscriptions implements `gregale events subscriptions <app>`.
// It gives simple users a direct answer to "what will receive this event?"
// without requiring them to inspect deployment YAML or internal tables.
func cmdEventsSubscriptions(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscriptions", flag.ContinueOnError)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscriptions <app>", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListEventSubscriptions(context.Background(), positional[0])
	if err != nil {
		return printErr("Could not list event subscriptions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Subscriptions) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no event subscriptions)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID\tSOURCE\tTYPE\tFILTER\tENABLED\tUPDATED")
	for _, subscription := range resp.Subscriptions {
		filter := string(subscription.Filter)
		if filter == "" {
			filter = "{}"
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%t\t%s\n",
			subscription.ID,
			subscription.Source,
			subscription.Type,
			filter,
			subscription.Enabled,
			subscription.UpdatedAt.Format(time.RFC3339),
		)
	}
	return 0
}

// cmdEventsPublish implements `gregale events publish`. The server owns
// account_id and accepts only JSON event data; --data follows the same
// inline/@file/stdin convention as invoke and queue commands.
func cmdEventsPublish(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events publish", flag.ContinueOnError)
	id := fs.String("id", "", "stable event id (defaults to a generated UUID)")
	source := fs.String("source", "", "event source (or the first positional argument)")
	typ := fs.String("type", "", "event type (or the second positional argument)")
	data := fs.String("data", "", "JSON event data (inline | @file | - for stdin; required)")
	occurredAt := fs.String("time", "", "event time (RFC3339; defaults to server time)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) > 2 {
		PrintUsage(os.Stderr, "usage: gregale events publish [SOURCE TYPE] --data <json|@file|-> [--id ID] [--time RFC3339]", "events")
		return 1
	}
	if len(positional) >= 1 {
		if *source != "" {
			return printErr("Invalid arguments", fmt.Errorf("source provided both positionally and with --source"))
		}
		*source = positional[0]
	}
	if len(positional) == 2 {
		if *typ != "" {
			return printErr("Invalid arguments", fmt.Errorf("type provided both positionally and with --type"))
		}
		*typ = positional[1]
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*source) == "" || strings.TrimSpace(*typ) == "" || strings.TrimSpace(*data) == "" {
		PrintUsage(os.Stderr, "usage: gregale events publish [SOURCE TYPE] --data <json|@file|-> [--id ID] [--time RFC3339]", "events")
		return 1
	}
	eventID := strings.TrimSpace(*id)
	if eventID == "" {
		eventID = uuid.NewString()
	}
	body, err := resolvePayload(*data)
	if err != nil {
		return printErr("Invalid --data", err)
	}
	if len(body) == 0 || !json.Valid(body) {
		return printErr("Invalid --data", fmt.Errorf("must be a non-empty JSON value"))
	}

	var eventTime *time.Time
	if strings.TrimSpace(*occurredAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *occurredAt)
		if parseErr != nil {
			return printErr("Invalid --time", fmt.Errorf("must be RFC3339: %w", parseErr))
		}
		eventTime = &parsed
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PublishEvent(context.Background(), api.PublishEventRequest{
		ID:     eventID,
		Source: strings.TrimSpace(*source),
		Type:   strings.TrimSpace(*typ),
		Time:   eventTime,
		Data:   json.RawMessage(body),
	})
	if err != nil {
		return printErr("Event publish failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Event %s accepted for account %s.", resp.ID, resp.AccountID)
	return 0
}
