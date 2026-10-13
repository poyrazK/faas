package main

import (
	"context"
	"flag"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdCommitHistory(args []string) int {
	fs := newFlagSet("commit history", flag.ContinueOnError)
	before := fs.String("before", "", "continuation cursor from next_before")
	limit := fs.Int("limit", api.CommitPublicationHistoryPageDefault, "maximum accepted publications per page")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *limit < 1 || *limit > api.CommitPublicationHistoryPageMax {
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid source ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := client.ListCommitPublications(ctx, positional[0], *before, *limit)
	if err != nil {
		return printErr("Cannot read Commit publication history", err)
	}
	return jsonOut(writeJSON(out))
}
