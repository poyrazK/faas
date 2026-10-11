package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRealtimeReadProgress(args []string, advance bool) int {
	fs := newFlagSet("realtime read-progress", flag.ContinueOnError)
	principal := fs.String("principal", "", "verified user principal (required)")
	channel := fs.String("channel", "", "channel; omit for principal inbox")
	seq := fs.Int64("sequence", -1, "mark seen through this sequence")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 2 || api.ValidateRealtimePrincipal(*principal) != nil || (advance && *seq < 0) || (!advance && *seq != -1) {
		return printErr("Invalid read progress", fmt.Errorf("use APP ENDPOINT --principal ID [--channel NAME]; mark-read also requires --sequence N"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var target *int64
	if advance {
		target = seq
	}
	result, err := client.ManagedRealtimeReadProgress(context.Background(), fs.Arg(0), fs.Arg(1), *channel, *principal, *channel == "", target)
	if err != nil {
		return printErr("Could not access read progress", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Read through %d; %d unread retained message(s), latest sequence %d.", result.Sequence, result.Unread, result.LatestSequence)
	if result.HistoryUnavailable {
		PrintWarn(osStderr, "Older unread history is unavailable; the count covers retained messages only.")
	}
	return 0
}
