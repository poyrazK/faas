package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageCases(args []string, retry bool) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events lineage cases", flag.ContinueOnError)
	limit := fs.Int("limit", api.EventExecutionLineageCasesPageDefault, "page size")
	after := fs.Int64("after", 0, "exclusive outbox cursor")
	requestID := fs.String("request-id", "", "stable retry request UUID")
	diagnosticID := fs.String("expected-diagnostic-id", "", "incarnation UUID for a backfilled case")
	revision := fs.Int64("expected-revision", 0, "reviewed case revision")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	invalidFlag := false
	fs.Visit(func(f *flag.Flag) {
		if retry && (f.Name == "limit" || f.Name == "after") || !retry && (f.Name == "request-id" || f.Name == "expected-revision" || f.Name == "expected-diagnostic-id") {
			invalidFlag = true
		}
	})
	if invalidFlag {
		return printErr("Invalid flag", fmt.Errorf("flag is not supported by this lineage command"))
	}
	n := 2
	if retry {
		n = 3
	}
	if len(positional) != n || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-cases APP SUB [--limit N] [--cursor ID] | subscription-lineage-retry APP SUB OUTBOX --request-id UUID --expected-revision N [--expected-diagnostic-id UUID]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if retry {
		id, err := strconv.ParseInt(positional[2], 10, 64)
		if err != nil || id <= 0 || *revision <= 0 || *requestID == "" {
			return printErr("Invalid retry guard", fmt.Errorf("positive outbox ID, revision and request UUID are required"))
		}
		out, err := client.RetryEventExecutionLineageCase(context.Background(), positional[0], positional[1], id, api.RetryEventExecutionLineageCaseRequest{RequestID: *requestID, ExpectedRevision: *revision, ExpectedDiagnosticID: *diagnosticID})
		if err != nil {
			return printErr("Lineage retry failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Case %d | %s (%s) | revision %d | retries %d\n", out.OutboxID, oneLine(out.Status), oneLine(out.Reason), out.Revision, out.RetryCount)
		return 0
	}
	out, err := client.ListEventExecutionLineageCases(context.Background(), positional[0], positional[1], *after, *limit)
	if err != nil {
		return printErr("Lineage cases failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	for _, c := range out.Cases {
		_, _ = fmt.Fprintf(osStdout, "Case %d | %s (%s) | revision %d | root %s | diagnostic %s\n", c.OutboxID, oneLine(c.Status), oneLine(c.Reason), c.Revision, oneLine(c.RootInvocationID), oneLine(c.DiagnosticID))
	}
	if out.NextAfter > 0 {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %d\n", out.NextAfter)
	}
	return 0
}
