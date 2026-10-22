package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdCommitRecoveryPlan(args []string) int {
	fs := newFlagSet("commit recovery-plan", flag.ContinueOnError)
	path := fs.String("report", "", "saved delivery report JSON")
	pages := fs.Int("recipient-pages", 1, "recipient pages per publication (1..20)")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall read timeout")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *path == "" || *pages < 1 || *pages > api.CommitDeliveryReportPagesMax || *timeout <= 0 || *timeout > 24*time.Hour {
		return 1
	}
	saved, err := readCommitDeliveryReport(*path)
	if err != nil {
		return printErr("Cannot read saved report", err)
	}
	if saved.SourceID != positional[0] {
		return printErr("Saved report source mismatch", fmt.Errorf("use the original source UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	out, err := client.PlanCommitRecovery(ctx, saved, *pages)
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	return 0
}
