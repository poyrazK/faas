package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsBacklog(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events backlog", flag.ContinueOnError)
	app := fs.String("app", "", "filter by owned app slug")
	sub := fs.String("subscription-id", "", "filter by captured or backfilled recipient identifier")
	kind := fs.String("consumer-kind", "", "application or workflow")
	origin := fs.String("origin", "", "acceptance or backfill")
	state := fs.String("state", "", "pending or processing")
	reason := fs.String("waiting-reason", "", "filter by waiting reason, e.g. ordering_blocked")
	scope := fs.String("capacity-scope", "", "consumer, app or account")
	age := fs.Duration("min-age", 0, "minimum age since acceptance (whole seconds, e.g. 10m)")
	after := fs.String("after", "", "opaque recipient continuation cursor")
	consumersAfter := fs.String("consumers-after", "", "opaque consumer continuation cursor")
	limit := fs.Int("limit", api.EventBacklogPageDefault, "recipients per page (1..200)")
	consumerLimit := fs.Int("consumer-limit", api.EventBacklogPageDefault, "consumers per page (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	options := api.EventBacklogOptions{EventBacklogFilters: api.EventBacklogFilters{App: *app, SubscriptionID: *sub, ConsumerKind: *kind, Origin: *origin, WaitingReason: *reason, State: *state, CapacityScope: *scope, MinAgeSeconds: int64(*age / time.Second)}, After: *after, ConsumersAfter: *consumersAfter, Limit: *limit, ConsumerLimit: *consumerLimit}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || *age < 0 || *age%time.Second != 0 || options.Validate() != nil || validateCLILimit("limit", *limit, api.EventBacklogPageMax) != nil || validateCLILimit("consumer-limit", *consumerLimit, api.EventBacklogPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events backlog [--app APP] [--subscription-id ID] [--consumer-kind application|workflow] [--origin acceptance|backfill] [--state STATE] [--waiting-reason REASON] [--capacity-scope SCOPE] [--min-age 10m] [--after CURSOR] [--consumers-after CURSOR] [--limit N] [--consumer-limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetEventBacklog(context.Background(), options)
	if err != nil {
		return printErr("Event backlog lookup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	writeEventBacklog(result)
	return 0
}

func writeEventBacklog(r api.EventBacklogResponse) {
	_, _ = fmt.Fprintf(osStdout, "Observed: %s | coverage: %s\n", r.ObservedAt.Format(time.RFC3339), oneLine(r.Coverage))
	if r.UnattributedReceipts > 0 {
		_, _ = fmt.Fprintf(osStdout, "Legacy receipts without captured recipients: %d (account-wide; age filter applies)\n", r.UnattributedReceipts)
	}
	_, _ = fmt.Fprintln(osStdout, "APP\tKIND\tSUBSCRIPTION\tWAITING\tCAPACITY WAITING\tORDERING WAITING\tOLDEST AGE")
	for _, c := range r.Consumers {
		app := c.AppSlug
		if app == "" {
			app = c.AppID
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%d\t%d\t%.0fs\n", oneLine(app), oneLine(c.ConsumerKind), oneLine(c.SubscriptionID), c.WaitingRecipients, c.CapacityWaitingRecipients, c.OrderingWaitingRecipients, c.OldestAgeSeconds)
	}
	_, _ = fmt.Fprintln(osStdout, "APP\tKIND\tORIGIN\tWORKFLOW\tSUBSCRIPTION\tSOURCE\tEVENT\tSTATE\tREASON\tAGE\tDEFERRALS\tNEXT ATTEMPT")
	if len(r.Recipients) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No matching waiting recipients on this page.")
	}
	for _, e := range r.Recipients {
		app := e.AppSlug
		if app == "" {
			app = e.AppID
		}
		next := "-"
		if e.NextAttemptAt != nil {
			next = e.NextAttemptAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%.0fs\t%d\t%s\n", oneLine(app), oneLine(e.ConsumerKind), oneLine(e.Origin), oneLine(e.WorkflowName), oneLine(e.SubscriptionID), oneLine(e.EventSource), oneLine(e.EventID), oneLine(e.State), oneLine(e.WaitingReason), e.PendingAgeSeconds, e.CapacityDeferrals, next)
		if b := e.OrderingBlocker; b != nil {
			retry := "-"
			if b.NextAttemptAt != nil {
				retry = b.NextAttemptAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(osStdout, "  Blocked by %s / %s subscription %s (%s, %.0fs old, next attempt %s)\n", oneLine(b.EventSource), oneLine(b.EventID), oneLine(b.SubscriptionID), oneLine(b.State), b.AgeSeconds, retry)
		}
	}
	if r.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next recipient page: --after %s\n", r.NextAfter)
	}
	if r.NextConsumersAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next consumer page: --consumers-after %s\n", r.NextConsumersAfter)
	}
}
