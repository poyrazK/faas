package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsInspect(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events inspect", flag.ContinueOnError)
	source := fs.String("source", "", "published event source")
	id := fs.String("id", "", "published event id")
	after := fs.String("after", "", "opaque next_after recipient cursor")
	limit := fs.Int("limit", 100, "recipients per page (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*source) == "" || strings.TrimSpace(*id) == "" || validateCLILimit("limit", *limit, api.EventReceiptPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events inspect --source SOURCE --id ID [--after CURSOR] [--limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	receipt, err := client.GetEventReceipt(context.Background(), strings.TrimSpace(*source), strings.TrimSpace(*id), *after, *limit)
	if err != nil {
		return printErr("Event receipt lookup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	writeEventReceipt(receipt)
	return 0
}

func writeEventReceipt(receipt api.EventReceiptResponse) {
	_, _ = fmt.Fprintf(osStdout, "Event %s from %s (%s)\nAccepted: %s\n", oneLine(receipt.EventID), oneLine(receipt.EventSource), oneLine(receipt.EventType), receipt.AcceptedAt.Format(time.RFC3339))
	if !receipt.SnapshotCaptured {
		_, _ = fmt.Fprintln(osStdout, "Recipient snapshot was not retained for this legacy event.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Recipients: %d | pending: %d | processing: %d | filtered: %d | enqueued: %d | failed: %d\n",
		receipt.RecipientCount, receipt.RoutingSummary["pending"], receipt.RoutingSummary["processing"], receipt.RoutingSummary["filtered"], receipt.RoutingSummary["enqueued"], receipt.RoutingSummary["failed"])
	if receipt.RoutingSettledAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Routing settled: %s\n", receipt.RoutingSettledAt.Format(time.RFC3339))
	}
	if receipt.RetainUntil != nil {
		_, _ = fmt.Fprintf(osStdout, "Receipt retained until: %s\n", receipt.RetainUntil.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(osStdout, "APP\tSUBSCRIPTION\tROUTING\tROUTE ATTEMPTS\tHANDLER\tHANDLER ATTEMPTS\tNEXT RETRY\tRECOVERY\tERROR")
	for _, entry := range receipt.Recipients {
		writeEventReceiptRecipient(entry)
	}
	if receipt.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s\n", receipt.NextAfter)
	}
}

func writeEventReceiptRecipient(entry api.EventReceiptRecipientResponse) {
	handler, attempts, lastError, next := "-", 0, entry.Routing.LastError, entry.Routing.NextAttemptAt
	if entry.Execution != nil {
		handler, attempts, lastError, next = entry.Execution.State, entry.Execution.Attempts, entry.Execution.LastError, entry.Execution.NextAttemptAt
	} else if entry.Cancellation != nil {
		handler = fmt.Sprintf("cancel_pending (%d cancelled)", entry.Cancellation.CancelledCount)
	} else if entry.ExecutionUnavailable == "record_unavailable" {
		handler = entry.ExecutionUnavailable
	}
	nextRetry := "-"
	if next != nil {
		nextRetry = next.Format(time.RFC3339)
	}
	recovery := make([]string, 0, len(entry.RecoveryActions))
	for _, action := range entry.RecoveryActions {
		recovery = append(recovery, action.Kind)
	}
	app := entry.AppSlug
	if app == "" {
		app = entry.AppID
	}
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%s\t%d\t%s\t%s\t%s\n", oneLine(app), oneLine(entry.SubscriptionID), oneLine(entry.Routing.State), entry.Routing.Attempts, oneLine(handler), attempts, nextRetry, oneLine(strings.Join(recovery, ",")), oneLine(lastError))
}
