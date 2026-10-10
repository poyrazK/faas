package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdCommitDeliveryReport(args []string) int {
	fs := newFlagSet("commit delivery-report", flag.ContinueOnError)
	before := fs.String("before", "", "history next_before cursor")
	subscription := fs.String("subscription-id", "", "filter original application subscriber")
	limit := fs.Int("limit", api.CommitPublicationHistoryPageDefault, "publications per page")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall read timeout")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *limit < 1 || *limit > api.CommitPublicationHistoryPageMax || *timeout <= 0 || *timeout > 24*time.Hour {
		return 1
	}
	for _, id := range []string{positional[0], *subscription} {
		if id == "" {
			continue
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return printErr("Invalid Commit identity", api.ErrValidation("identity must be a nonzero UUID"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	out, err := client.GetCommitDeliveryReport(ctx, positional[0], *before, *subscription, *limit)
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	return 0
}
