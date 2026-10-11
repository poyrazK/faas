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

func cmdEventsAttempts(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events attempts", flag.ContinueOnError)
	source := fs.String("source", "", "published event source")
	id := fs.String("id", "", "published event id")
	sub := fs.String("subscription", "", "captured subscription id")
	after := fs.String("after", "", "opaque next_after attempt cursor")
	limit := fs.Int("limit", 100, "attempts per page (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*source) == "" || strings.TrimSpace(*id) == "" || strings.TrimSpace(*sub) == "" || validateCLILimit("limit", *limit, api.EventReceiptPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events attempts --source SOURCE --id ID --subscription SUB [--after CURSOR] [--limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	history, err := client.GetEventReceiptAttempts(context.Background(), strings.TrimSpace(*source), strings.TrimSpace(*id), strings.TrimSpace(*sub), *after, *limit)
	if err != nil {
		return printErr("Event attempt history lookup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(history))
	}
	_, _ = fmt.Fprintf(osStdout, "Event %s from %s | subscription: %s | original invocation: %s\n", oneLine(history.EventID), oneLine(history.EventSource), oneLine(history.SubscriptionID), oneLine(history.OriginalInvocationID))
	_, _ = fmt.Fprintln(osStdout, "History contains recorded, retained attempts. An unknown outcome does not prove whether the handler ran.")
	_, _ = fmt.Fprintln(osStdout, "INVOCATION\tGENERATION\tATTEMPT\tOUTCOME\tSTARTED\tFINISHED\tNEXT RETRY\tERROR")
	for _, attempt := range history.Attempts {
		_, _ = fmt.Fprintf(osStdout, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", oneLine(attempt.InvocationID), attempt.ReplayGeneration, attempt.Attempt, oneLine(attempt.Outcome), attempt.StartedAt.Format(time.RFC3339), attemptHistoryTime(attempt.FinishedAt), attemptHistoryTime(attempt.NextAttemptAt), oneLine(attempt.ErrorDetail))
	}
	if history.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s\n", history.NextAfter)
	}
	return 0
}

func attemptHistoryTime(at *time.Time) string {
	if at == nil {
		return "-"
	}
	return at.Format(time.RFC3339)
}
