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
		PrintUsage(os.Stderr, "usage: gregale events <subscription-circuit-status|subscription-circuit-set|subscription-circuit-disable|subscription-circuit-reset|subscription-pause|subscription-resume|subscription-status|recovery-preflight|recovery-health|notification-retry-reconcile|notification-retry-plan|notification-retry-apply|notification-retry-backlog|recovery-notification-retry-history|recovery-notification-retry-preview|recovery-notification-retry|recovery-notifications|recovery-history|recovery-list|recovery-preview|recovery-create|recovery-status|recovery-items|recovery-cancel|recovery-pause|recovery-resume|recovery-rate|preview|replay-preview|workflow-replay-preview|backfill|workflow-backfill|backfill-status|backfill-items|backfill-retry|publish|publish-batch|retention|backlog|inspect|recover|attempts|subscriptions|deliveries|fanout-history|replay|replay-retryable>", "events")
		return 1
	}
	switch args[0] {
	case "subscription-execution-health":
		return cmdEventsSubscriptionExecutionHealth(args[1:])
	case "schema-rollout-preview":
		return cmdEventsSchemaRollout(args[1:])
	case "subscription-circuit-status":
		return cmdEventsSubscriptionCircuit(args[1:], "status")
	case "subscription-circuit-set":
		return cmdEventsSubscriptionCircuit(args[1:], "set")
	case "subscription-circuit-disable":
		return cmdEventsSubscriptionCircuit(args[1:], "disable")
	case "subscription-circuit-reset":
		return cmdEventsSubscriptionCircuit(args[1:], "reset")
	case "subscription-pause":
		return cmdEventsSubscriptionControl(args[1:], "pause")
	case "subscription-resume":
		return cmdEventsSubscriptionControl(args[1:], "resume")
	case "subscription-versions-status":
		return cmdEventsSubscriptionVersions(args[1:], "status")
	case "subscription-versions-set":
		return cmdEventsSubscriptionVersions(args[1:], "set")
	case "subscription-versions-reset":
		return cmdEventsSubscriptionVersions(args[1:], "reset")
	case "subscription-retry-status":
		return cmdEventsSubscriptionRetry(args[1:], "status")
	case "subscription-retry-set":
		return cmdEventsSubscriptionRetry(args[1:], "set")
	case "subscription-retry-reset":
		return cmdEventsSubscriptionRetry(args[1:], "reset")
	case "subscription-health":
		return cmdEventsSubscriptionHealth(args[1:])
	case "subscription-status":
		return cmdEventsSubscriptionControl(args[1:], "status")
	case "recovery-preflight":
		return cmdEventsRecoveryPreflight(args[1:])
	case "notification-retry-reconcile":
		return cmdEventsNotificationRetryReconcile(args[1:])
	case "notification-retry-plan":
		return cmdEventsNotificationRetryBatch(args[1:], false)
	case "notification-retry-apply":
		return cmdEventsNotificationRetryBatch(args[1:], true)
	case "notification-retry-backlog":
		return cmdEventsNotificationRetryBacklog(args[1:])
	case "recovery-notification-retry-history":
		return cmdEventsRecoveryNotificationRetryHistory(args[1:])
	case "recovery-notification-retry-preview":
		return cmdEventsRecoveryNotificationRetryPreview(args[1:])
	case "recovery-notification-retry":
		return cmdEventsRecoveryNotificationRetry(args[1:])
	case "recovery-notifications":
		return cmdEventsRecoveryNotifications(args[1:])
	case "recovery-health":
		return cmdEventsRecoveryHealth(args[1:])
	case "recovery-history":
		return cmdEventsRecoveryHistory(args[1:])
	case "recovery-list":
		return cmdEventsRecoveryList(args[1:])
	case "recovery-preview":
		return cmdEventsBulkRecovery(args[1:], true)
	case "recovery-create":
		return cmdEventsBulkRecovery(args[1:], false)
	case "recovery-status":
		return cmdEventsRecoveryJob(args[1:], "status")
	case "recovery-cancel":
		return cmdEventsRecoveryJob(args[1:], "cancel")
	case "recovery-pause":
		return cmdEventsRecoveryJob(args[1:], "pause")
	case "recovery-resume":
		return cmdEventsRecoveryJob(args[1:], "resume")
	case "recovery-rate":
		return cmdEventsRecoveryRate(args[1:])
	case "recovery-items":
		return cmdEventsRecoveryItems(args[1:])
	case "backlog":
		return cmdEventsBacklog(args[1:])
	case "preview":
		return cmdEventsPreview(args[1:])
	case "replay-preview":
		return cmdEventsReplayPreview(args[1:])
	case "workflow-replay-preview":
		return cmdEventsWorkflowReplayPreview(args[1:])
	case "backfill":
		return cmdEventsBackfill(args[1:])
	case "workflow-backfill":
		return cmdEventsWorkflowBackfill(args[1:])
	case "backfill-status":
		return cmdEventsBackfillStatus(args[1:])
	case "backfill-items":
		return cmdEventsBackfillItems(args[1:])
	case "backfill-retry":
		return cmdEventsBackfillRetry(args[1:])
	case "inspect":
		return cmdEventsInspect(args[1:])
	case "recover":
		return cmdEventsRecover(args[1:])
	case "attempts":
		return cmdEventsAttempts(args[1:])
	case "retention":
		return cmdEventsRetention(args[1:])
	case "publish-batch":
		return cmdEventsPublishBatch(args[1:])
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
	flags, positional := splitArgsForFlags(args, "yes")
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
	flags, positional := splitArgsForFlags(args, "allow-expired")
	fs := newFlagSet("events replay", flag.ContinueOnError)
	allowExpired := fs.Bool("allow-expired", false, "explicitly bypass delivery age for this replay generation")
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
		AllowExpired: *allowExpired, EventID: strings.TrimSpace(*eventID), EventSource: strings.TrimSpace(*eventSource),
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
	body, err := resolveJSONFlag("--data", *data)
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
	_, _ = fmt.Fprintf(osStdout, "Would deliver: %d  |  Filtered: %d  |  Schema version mismatches: %d  |  Other mismatches: %d\n", resp.MatchedCount, resp.FilterMismatchCount, resp.SchemaVersionMismatchCount, resp.OtherMismatchCount)
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
	flags, positional := splitArgsForFlags(args, "all")
	fs := newFlagSet("events deliveries", flag.ContinueOnError)
	eventSource := fs.String("event-source", "", "narrow event filter to one published source; requires --event-id")
	eventID := fs.String("event-id", "", "filter by published event id")
	deliveryState := fs.String("state", "", "filter by delivery state; failed includes recipient fanout failures")
	before := fs.String("before", "", "pagination cursor (NextBefore from a prior call)")
	fanoutBefore := fs.String("fanout-before", "", "pagination cursor (NextFanoutBefore from a prior call)")
	limit := fs.Int("limit", 20, "page size per stream (1..200)")
	all := fs.Bool("all", false, "walk both streams using their independent cursors")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) ||
		(strings.TrimSpace(*eventSource) != "" && strings.TrimSpace(*eventID) == "") ||
		validateCLILimit("limit", *limit, 200) != nil {
		PrintUsage(os.Stderr, "usage: gregale events deliveries <app> [--event-source SOURCE --event-id ID] [--state STATE] [--before CURSOR] [--fanout-before CURSOR] [--limit N] [--all]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := collectEventDeliveryPages(context.Background(), *before, *fanoutBefore, *all, func(ctx context.Context, before, fanoutBefore string) (api.EventDeliveryListResponse, error) {
		return client.ListEventDeliveriesPageByEventIdentity(ctx, positional[0],
			strings.TrimSpace(*eventSource), strings.TrimSpace(*eventID), *deliveryState, before, fanoutBefore, *limit)
	})
	if err != nil {
		return printErr("Could not list event deliveries", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Deliveries) == 0 && len(resp.FanoutFailures) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no event deliveries)")
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
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more invocations — pass --before %s\n", resp.NextBefore)
	}
	if resp.NextFanoutBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more fanout failures — pass --fanout-before %s\n", resp.NextFanoutBefore)
	}
	return 0
}

// cmdEventsFanoutHistory shows immutable pre-invocation routing outcomes and
// explicit replay requests for one event recipient set.
func cmdEventsFanoutHistory(args []string) int {
	flags, positional := splitArgsForFlags(args, "all")
	fs := newFlagSet("events fanout-history", flag.ContinueOnError)
	eventSource := fs.String("event-source", "", "published event source")
	eventID := fs.String("event-id", "", "published event id")
	subscriptionID := fs.String("subscription-id", "", "narrow history to one recipient")
	before := fs.String("before", "", "pagination cursor (NextBefore from a prior call)")
	limit := fs.Int("limit", 20, "page size (1..200)")
	fs.StringVar(before, "cursor", "", "alias for --before")
	all := fs.Bool("all", false, "walk every page using --limit and --cursor")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) ||
		strings.TrimSpace(*eventSource) == "" || strings.TrimSpace(*eventID) == "" ||
		validateCLILimit("limit", *limit, 200) != nil {
		PrintUsage(os.Stderr, "usage: gregale events fanout-history <app> --event-source SOURCE --event-id ID [--subscription-id ID] [--cursor CURSOR] [--limit N] [--all]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var resp api.EventFanoutAttemptHistoryResponse
	items, next, err := collectListPages(context.Background(), *before, *all, func(ctx context.Context, cursor string) ([]api.EventFanoutAttemptResponse, string, error) {
		page, err := client.ListEventFanoutAttemptHistory(ctx, positional[0],
			strings.TrimSpace(*eventSource), strings.TrimSpace(*eventID), strings.TrimSpace(*subscriptionID), cursor, *limit)
		resp = page
		return page.History, page.NextBefore, err
	})
	resp.History, resp.NextBefore = items, next
	if err != nil {
		return printErr("Could not list event fanout history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			api.EventFanoutAttemptHistoryResponse
			NextCursor string `json:"next_cursor,omitempty"`
		}{resp, next}))
	}
	if len(resp.History) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no event fanout history)")
	}
	_, _ = fmt.Fprintln(osStdout, "EVENT\tSOURCE\tSUBSCRIPTION\tACTION\tSTATE\tATTEMPT\tFAILURE_CODE\tRETRYABLE\tRECORDED\tERROR")
	for _, row := range resp.History {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%t\t%s\t%s\n",
			oneLine(resp.EventID), oneLine(resp.EventSource), oneLine(row.SubscriptionID),
			oneLine(row.Action), oneLine(row.State), row.AttemptNumber, oneLine(row.FailureCode),
			row.Retryable, row.OccurredAt.Format(time.RFC3339), oneLine(row.LastError))
	}
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more history — pass --cursor %s\n", resp.NextBefore)
	}
	return 0
}

// cmdEventsSubscriptions implements `gregale events subscriptions <app>`.
// It gives simple users a direct answer to "what will receive this event?"
// without requiring them to inspect deployment YAML or internal tables.
func cmdEventsSubscriptions(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscriptions", flag.ContinueOnError)
	app := fs.String("app", "", appSlugFlagUsage)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	positional, mergeErr := mergeAppFlag(positional, *app, 1)
	if mergeErr != nil || len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
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
	_, _ = fmt.Fprintln(osStdout, "ID\tSOURCE\tTYPE\tFILTER\tORDERED\tENABLED\tUPDATED\tVERSIONS")
	for _, subscription := range resp.Subscriptions {
		filter := string(subscription.Filter)
		if filter == "" {
			filter = "{}"
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%t\t%t\t%s\t%s\n",
			subscription.ID,
			subscription.Source,
			subscription.Type,
			filter,
			subscription.Ordered,
			subscription.Enabled,
			subscription.UpdatedAt.Format(time.RFC3339),
			eventSchemaVersionsLabel(subscription.SchemaVersions),
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
	body, err := resolveJSONFlag("--data", *data)
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
	if resp.ReceiptURL != "" {
		_, _ = fmt.Fprintf(osStdout, "Receipt: %s\n", resp.ReceiptURL)
	}
	return 0
}
