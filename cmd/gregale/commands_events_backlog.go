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
	sub := fs.String("subscription-id", "", "filter by captured recipient identifier")
	state := fs.String("state", "", "pending or processing")
	scope := fs.String("capacity-scope", "", "consumer, app or account")
	age := fs.Duration("min-age", 0, "minimum age since acceptance (whole seconds, e.g. 10m)")
	after := fs.String("after", "", "opaque recipient continuation cursor")
	consumersAfter := fs.String("consumers-after", "", "opaque consumer continuation cursor")
	limit := fs.Int("limit", api.EventBacklogPageDefault, "recipients per page (1..200)")
	consumerLimit := fs.Int("consumer-limit", api.EventBacklogPageDefault, "consumers per page (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	options := api.EventBacklogOptions{EventBacklogFilters: api.EventBacklogFilters{App: *app, SubscriptionID: *sub, State: *state, CapacityScope: *scope, MinAgeSeconds: int64(*age / time.Second)}, After: *after, ConsumersAfter: *consumersAfter, Limit: *limit, ConsumerLimit: *consumerLimit}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || *age < 0 || *age%time.Second != 0 || options.Validate() != nil || validateCLILimit("limit", *limit, api.EventBacklogPageMax) != nil || validateCLILimit("consumer-limit", *consumerLimit, api.EventBacklogPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events backlog [--app APP] [--subscription-id ID] [--state STATE] [--capacity-scope SCOPE] [--min-age 10m] [--after CURSOR] [--consumers-after CURSOR] [--limit N] [--consumer-limit N]", "events")
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
	_, _ = fmt.Fprintln(osStdout, "APP\tSUBSCRIPTION\tWAITING\tCAPACITY WAITING\tOLDEST AGE")
	for _, c := range r.Consumers {
		app := c.AppSlug
		if app == "" {
			app = c.AppID
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%d\t%d\t%.0fs\n", oneLine(app), oneLine(c.SubscriptionID), c.WaitingRecipients, c.CapacityWaitingRecipients, c.OldestAgeSeconds)
	}
	_, _ = fmt.Fprintln(osStdout, "APP\tSUBSCRIPTION\tSOURCE\tEVENT\tSTATE\tREASON\tAGE\tDEFERRALS\tNEXT ATTEMPT")
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
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%s\t%.0fs\t%d\t%s\n", oneLine(app), oneLine(e.SubscriptionID), oneLine(e.EventSource), oneLine(e.EventID), oneLine(e.State), oneLine(e.WaitingReason), e.PendingAgeSeconds, e.CapacityDeferrals, next)
	}
	if r.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next recipient page: --after %s\n", r.NextAfter)
	}
	if r.NextConsumersAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next consumer page: --consumers-after %s\n", r.NextConsumersAfter)
	}
}
