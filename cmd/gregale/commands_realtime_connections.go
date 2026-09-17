package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/realtime"
)

const realtimeConnectionsCLILimitMax = 1000

func cmdRealtimeConnections(args []string) int {
	args = normalizeRealtimeConnectionsArgs(args)
	fs := newFlagSet("realtime connections", flag.ContinueOnError)
	limit := fs.Int("limit", 100, "maximum connections to return (1-1000)")
	channel := fs.String("channel", "", "only connections subscribed to this channel")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(osStderr, "usage: gregale realtime connections APP_SLUG ENDPOINT_ID [--channel CHANNEL] [--limit N]", "realtime")
		return 1
	}
	if *limit < 1 || *limit > realtimeConnectionsCLILimitMax {
		return printErr("Invalid connection limit", fmt.Errorf("must be between 1 and %d", realtimeConnectionsCLILimitMax))
	}
	if *channel != "" && !realtime.ValidateChannel(*channel) {
		return printErr("Invalid channel", fmt.Errorf("channel must be non-empty, at most 256 bytes, and contain no '/', '?', '#', or whitespace padding"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.ListManagedRealtimeConnections(context.Background(), fs.Arg(0), fs.Arg(1), *channel, *limit)
	if err != nil {
		return printErr("Could not list realtime connections", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	if response.Partial {
		PrintWarn(osStderr, fmt.Sprintf("Realtime connection inventory is partial: %d node(s) unavailable.", response.NodesUnavailable))
	}
	if len(response.Connections) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No live realtime connections.")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID                                      PRINCIPAL                 CONNECTED                 LAST SEEN                 CHANNELS")
	for _, connection := range response.Connections {
		channels := strings.Join(connection.Channels, ",")
		_, _ = fmt.Fprintf(osStdout, "%-39s %-25s %-25s %-25s %s\n", connection.ID, connection.Principal, connection.ConnectedAt, connection.LastSeenAt, channels)
	}
	if response.Truncated {
		_, _ = fmt.Fprintf(osStdout, "Showing %d connection(s); increase --limit to see more.\n", response.Limit)
	}
	return 0
}

func normalizeRealtimeConnectionsArgs(args []string) []string {
	return normalizeRealtimeValueArgs(args, map[string]bool{"--channel": true, "--limit": true}, nil)
}
