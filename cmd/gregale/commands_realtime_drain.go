package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

const realtimeDrainConnectionIDsMax = 100

func cmdRealtimeDrain(args []string) int {
	args = normalizeRealtimeDrainArgs(args)
	fs := newFlagSet("realtime drain", flag.ContinueOnError)
	reason := fs.String("reason", "", "reason recorded in the audit event")
	channel := fs.String("channel", "", "only connections subscribed to this channel")
	principal := fs.String("principal", "", "only connections for this principal")
	limit := fs.Int("limit", 100, "maximum connections to select (1-1000)")
	dryRun := fs.Bool("dry-run", false, "preview the selected connections without closing them")
	allowPartial := fs.Bool("allow-partial", false, "allow closing the reachable subset when some nodes are unavailable")
	var connectionIDs realtimeStringList
	fs.Var(&connectionIDs, "connection-id", "select a specific connection; may be repeated (max 100)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(osStderr, "usage: gregale realtime drain APP_SLUG ENDPOINT_ID --reason TEXT [--channel CHANNEL] [--principal PRINCIPAL] [--connection-id ID ...] [--limit N] [--dry-run] [--allow-partial]", "realtime")
		return 1
	}
	if strings.TrimSpace(*reason) == "" {
		return printErr("Invalid drain reason", fmt.Errorf("--reason is required"))
	}
	if *limit < 1 || *limit > realtimeConnectionsCLILimitMax {
		return printErr("Invalid connection limit", fmt.Errorf("must be between 1 and %d", realtimeConnectionsCLILimitMax))
	}
	if *channel != "" && !realtime.ValidateChannel(*channel) {
		return printErr("Invalid channel", fmt.Errorf("channel must be non-empty, at most 256 bytes, and contain no '/', '?', '#', or whitespace padding"))
	}
	if len(connectionIDs) > realtimeDrainConnectionIDsMax {
		return printErr("Too many connection IDs", fmt.Errorf("provide at most %d --connection-id values", realtimeDrainConnectionIDsMax))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.DrainManagedRealtimeConnections(context.Background(), fs.Arg(0), fs.Arg(1), api.ManagedRealtimeDrainRequest{
		Channel:       *channel,
		Principal:     *principal,
		ConnectionIDs: append([]string(nil), connectionIDs...),
		Limit:         *limit,
		Reason:        strings.TrimSpace(*reason),
		DryRun:        *dryRun,
		AllowPartial:  *allowPartial,
	})
	if err != nil {
		return printErr("Could not drain realtime connections", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	if response.Status == "running" {
		_, _ = fmt.Fprintf(osStdout, "Realtime drain accepted; operation %s is running.\n", response.OperationID)
		return 0
	}
	if response.Partial {
		PrintWarn(osStderr, fmt.Sprintf("Realtime connection inventory is partial: %d node(s) unavailable.", response.NodesUnavailable))
	}
	if response.DryRun {
		_, _ = fmt.Fprintf(osStdout, "Would close %d realtime connection(s).\n", response.Matched)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Closed %d realtime connection(s); %d already gone, %d failed.\n", response.Closed, response.Gone, response.Failed)
	}
	if response.Truncated {
		_, _ = fmt.Fprintf(osStdout, "Selection truncated at %d connection(s); increase --limit to select more.\n", response.Limit)
	}
	return 0
}

func normalizeRealtimeDrainArgs(args []string) []string {
	return normalizeRealtimeValueArgs(args,
		map[string]bool{"--reason": true, "--channel": true, "--principal": true, "--connection-id": true, "--limit": true},
		map[string]bool{"--dry-run": true, "--allow-partial": true})
}
