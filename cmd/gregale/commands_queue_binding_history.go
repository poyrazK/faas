package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

type queueBindingListClient interface {
	ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error)
	ListQueueBindingHistory(context.Context, string) ([]api.QueueBindingResponse, error)
}

func cmdQueueBindingList(client queueBindingListClient, args []string) int {
	fs := newFlagSet("queue bindings list", flag.ContinueOnError)
	includeRetired := fs.Bool("include-retired", false, "include retained binding UUIDs for reviewed recovery")
	flags, pos := splitArgsForFlags(args, "include-retired")
	if err := fs.Parse(flags); err != nil {
		PrintUsage(osStderr, "usage: gregale queue bindings list <slug> [--include-retired]", "queue")
		return 1
	}
	if pos = withLinkedApp(pos, 1); len(pos) != 1 {
		PrintUsage(osStderr, "usage: gregale queue bindings list <slug> [--include-retired]", "queue")
		return 1
	}
	var rows []api.QueueBindingResponse
	var err error
	if *includeRetired {
		rows, err = client.ListQueueBindingHistory(context.Background(), pos[0])
	} else {
		rows, err = client.ListQueueBindings(context.Background(), pos[0])
	}
	if err != nil {
		return printErr("Queue binding list failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(rows))
	}
	if len(rows) == 0 {
		// An empty list printed nothing, which read as a broken command
		// (prod hunt r63).
		_, _ = fmt.Fprintf(osStdout, "No queue bindings for %s. Add one with `gregale queue bindings create %s --name NAME --queue-name QUEUE`.\n", pos[0], pos[0])
		return 0
	}
	for _, row := range rows {
		retirement := ""
		if row.RetiredAt != nil {
			retirement = " retired=" + row.RetiredAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		_, _ = fmt.Fprintf(osStdout, "%-32s %-16s %-6s %-6s environment=%s enabled=%t max=%d%s\n", row.ID, row.Name, row.Mode, row.WorkloadClass, queueEnvironmentLabel(row.Environment), row.Enabled, row.MaxConcurrency, retirement)
	}
	return 0
}
