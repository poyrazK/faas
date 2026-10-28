package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageCaseBackfill(args []string, request bool) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events lineage case backfill", flag.ContinueOnError)
	var requestID, previousID string
	if request {
		fs.StringVar(&requestID, "request-id", "", "stable scan request UUID")
		fs.StringVar(&previousID, "expected-previous-request-id", "", "reviewed completed, cancelled or expired scan UUID")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-case-backfill APP SUB | subscription-lineage-case-backfill-request APP SUB --request-id UUID [--expected-previous-request-id UUID]", "events")
		return 1
	}
	if request && requestID == "" {
		return printErr("Invalid request", fmt.Errorf("--request-id is required"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventExecutionLineageCaseBackfill
	if request {
		out, err = client.RequestEventExecutionLineageCaseBackfill(context.Background(), positional[0], positional[1], api.RequestEventExecutionLineageCaseBackfillRequest{RequestID: requestID, ExpectedPreviousRequestID: previousID})
	} else {
		out, err = client.GetEventExecutionLineageCaseBackfill(context.Background(), positional[0], positional[1])
	}
	if err != nil {
		return printErr("Lineage diagnostic scan failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Scan %s | %s | cursor %d/%d\n", oneLine(out.RequestID), oneLine(out.Status), out.Cursor, out.ThroughOutboxID)
	_, _ = fmt.Fprintf(osStdout, "Scanned %d | recorded %d | existing %d | associated %d | unresolved %d | conflicts %d\n", out.Scanned, out.Recorded, out.Existing, out.Associated, out.Unresolved, out.Conflicts)
	return 0
}
