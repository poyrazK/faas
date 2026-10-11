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

func cmdEventsReplayPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events replay-preview", flag.ContinueOnError)
	sub := fs.String("subscription-id", "", "target ordinary application subscription UUID")
	from := fs.String("from", "", "inclusive acceptance timestamp (RFC3339)")
	until := fs.String("until", "", "exclusive acceptance timestamp (RFC3339)")
	after := fs.String("after", "", "opaque continuation cursor; keep the target and range unchanged")
	limit := fs.Int("limit", api.EventReplayPreviewPageDefault, "envelopes examined per page (1..100)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	start, startErr := time.Parse(time.RFC3339Nano, *from)
	end, endErr := time.Parse(time.RFC3339Nano, *until)
	subscriptionID, subErr := uuid.Parse(*sub)
	options := api.EventReplayPreviewOptions{From: start, Until: end, After: *after, Limit: *limit}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || startErr != nil || endErr != nil || subErr != nil || options.Validate() != nil || validateCLILimit("limit", *limit, api.EventReplayPreviewPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events replay-preview <app> --subscription-id UUID --from RFC3339 --until RFC3339 [--after CURSOR] [--limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.PreviewEventReplay(context.Background(), positional[0], subscriptionID.String(), options)
	if err != nil {
		return printErr("Event replay preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	writeEventReplayPreview(result)
	return 0
}

func writeEventReplayPreview(r api.EventReplayPreviewResponse) {
	_, _ = fmt.Fprintf(osStdout, "Replay preview for %s / %s (read-only)\n", oneLine(r.AppSlug), oneLine(r.Subscription.ID))
	_, _ = fmt.Fprintf(osStdout, "Acceptance window: [%s, %s) | fixed cutoff: %s\n", r.From.Format(time.RFC3339Nano), r.Until.Format(time.RFC3339Nano), r.CutoffAt.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintf(osStdout, "This page: examined %d | matched %d | already captured %d | filter mismatches %d | pattern mismatches %d | schema version mismatches %d\n", r.ScannedCount, r.MatchedCount, r.AlreadyCapturedCount, r.FilterMismatchCount, r.PatternMismatchCount, r.SchemaVersionMismatchCount)
	_, _ = fmt.Fprintf(osStdout, "Settled receipt retention: %s after routing settlement. Complete history is not guaranteed.\n", time.Duration(r.Retention.SettledRetentionSeconds)*time.Second)
	if r.Retention.EarliestRetainedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Earliest retained acceptance (account-wide): %s\n", r.Retention.EarliestRetainedAt.Format(time.RFC3339Nano))
	}
	_, _ = fmt.Fprintln(osStdout, "ACCEPTED\tSOURCE\tEVENT\tTYPE\tORIGINAL RECIPIENT\tDELIVERY EXPIRED")
	for _, m := range r.Matches {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%t\n", m.AcceptedAt.Format(time.RFC3339Nano), oneLine(m.EventSource), oneLine(m.EventID), oneLine(m.EventType), oneLine(m.OriginalRecipient), m.DeliveryExpired)
	}
	if r.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s (keep the same app, subscription and range)\n", r.NextAfter)
	}
}
