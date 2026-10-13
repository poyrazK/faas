package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageRetryJob(args []string, mode string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet(mode, flag.ContinueOnError)
	var limit int
	var after int64
	var reason, file, expectedCreated string
	switch mode {
	case "subscription-lineage-retry-preview":
		fs.IntVar(&limit, "limit", api.EventExecutionLineageCasesPageDefault, "page size")
		fs.Int64Var(&after, "after", 0, "exclusive cursor")
		fs.StringVar(&reason, "reason", "", "exact reason code")
	case "subscription-lineage-retry-job-cancel":
		fs.StringVar(&expectedCreated, "expected-created-at", "", "original creation timestamp from the reviewed job")
	case "subscription-lineage-retry-job-create":
		fs.StringVar(&file, "file", "", "JSON request path")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	n := 2
	if mode == "subscription-lineage-retry-job" || mode == "subscription-lineage-retry-job-cancel" {
		n = 3
	}
	if len(positional) != n || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events "+mode+" APP SUB [REQUEST_ID] [--file PATH] [--reason CODE] [--limit N] [--after ID] [--expected-created-at TIMESTAMP]", "events")
		return 1
	}
	var input lineageRetryRequestFile
	if mode == "subscription-lineage-retry-job-create" {
		var err error
		input, err = readLineageRetryRequestFile(file)
		if err == nil {
			err = input.validateSubscription(positional[1])
		}
		if err != nil {
			return printErr("Invalid job request or target", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if mode == "subscription-lineage-retry-preview" {
		out, err := client.PreviewEventExecutionLineageRetries(context.Background(), positional[0], positional[1], reason, after, limit)
		if err != nil {
			return printErr("Lineage retry preview failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		for _, c := range out.Cases {
			_, _ = fmt.Fprintf(osStdout, "Case %d | %s (%s) | revision %d | accepted %s\n", c.OutboxID, oneLine(c.Status), oneLine(c.Reason), c.Revision, c.AcceptedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
		}
		if out.NextAfter > 0 {
			_, _ = fmt.Fprintf(osStdout, "Next page: --after %d\n", out.NextAfter)
		}
		return 0
	}
	var out api.EventExecutionLineageRetryJob
	if mode == "subscription-lineage-retry-job-create" {
		if err := input.validateApp(context.Background(), client, positional[0]); err != nil {
			return printErr("Retry file app target mismatch", err)
		}
		out, err = client.CreateEventExecutionLineageRetryJob(context.Background(), positional[0], positional[1], input.Request)
	} else if mode == "subscription-lineage-retry-job-cancel" {
		created, e := time.Parse(time.RFC3339Nano, expectedCreated)
		if e != nil || created.IsZero() {
			return printErr("Invalid cancellation guard", fmt.Errorf("--expected-created-at must be the original RFC3339 creation timestamp from the reviewed job"))
		}
		out, err = client.CancelEventExecutionLineageRetryJob(context.Background(), positional[0], positional[1], positional[2], api.CancelEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: created})
	} else {
		out, err = client.GetEventExecutionLineageRetryJob(context.Background(), positional[0], positional[1], positional[2])
	}
	if err != nil {
		return printErr("Lineage retry job failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Job %s | %s | processed %d/%d\n", oneLine(out.RequestID), oneLine(out.Status), out.Processed, len(out.Items))
	for _, item := range out.Items {
		_, _ = fmt.Fprintf(osStdout, "Case %d | %s (%s)\n", item.Selection.OutboxID, oneLine(item.Status), oneLine(item.Reason))
	}
	return 0
}
