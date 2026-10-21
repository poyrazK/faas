package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageCaseBackfillControl(args []string, mode string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet(mode, flag.ContinueOnError)
	expected := fs.String("expected-created-at", "", "original RFC3339 creation timestamp from the reviewed scan")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events "+mode+" APP SUB REQUEST_ID --expected-created-at TIMESTAMP", "events")
		return 1
	}
	created, err := time.Parse(time.RFC3339Nano, *expected)
	if err != nil || created.IsZero() {
		return printErr("Invalid scan guard", fmt.Errorf("--expected-created-at must be the original creation timestamp from the reviewed scan"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.ControlEventExecutionLineageCaseBackfillRequest{ExpectedCreatedAt: created}
	var out api.EventExecutionLineageCaseBackfill
	switch mode {
	case "subscription-lineage-case-backfill-pause":
		out, err = client.PauseEventExecutionLineageCaseBackfill(context.Background(), positional[0], positional[1], positional[2], req)
	case "subscription-lineage-case-backfill-resume":
		out, err = client.ResumeEventExecutionLineageCaseBackfill(context.Background(), positional[0], positional[1], positional[2], req)
	case "subscription-lineage-case-backfill-cancel":
		out, err = client.CancelEventExecutionLineageCaseBackfill(context.Background(), positional[0], positional[1], positional[2], req)
	default:
		return printErr("Invalid scan control", fmt.Errorf("unsupported command"))
	}
	if err != nil {
		return printErr("Lineage scan control failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Scan %s | %s | cursor %d/%d | scanned %d | recorded %d\n", oneLine(out.RequestID), oneLine(out.Status), out.Cursor, out.ThroughOutboxID, out.Scanned, out.Recorded)
	return 0
}
