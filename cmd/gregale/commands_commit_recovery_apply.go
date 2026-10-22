package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"os"
	"os/signal"
	"time"
)

func cmdCommitRecoveryApply(args []string) int {
	fs := newFlagSet("commit recovery-apply", flag.ContinueOnError)
	path := fs.String("plan", "", "reviewed recovery plan JSON")
	yes := fs.Bool("yes", false, "confirm recovery submissions")
	limit := fs.Int("limit", api.CommitRecoveryApplyMax, "eligible items examined")
	pages := fs.Int("recipient-pages", 1, "receipt pages per item")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout")
	flags, positional := splitArgsForFlags(args, "yes")
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *path == "" || !*yes || *limit < 1 || *limit > api.CommitRecoveryApplyMax || *pages < 1 || *pages > api.CommitDeliveryReportPagesMax || *timeout <= 0 || *timeout > 24*time.Hour {
		return 1
	}
	f, err := openCustomerFile(*path)
	if err != nil {
		return printErr("Cannot read recovery plan", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.CommitDeliveryReportFileMaxBytes+1))
	if err != nil {
		return printErr("Cannot read recovery plan", err)
	}
	var plan api.CommitRecoveryPlan
	if len(raw) > api.CommitDeliveryReportFileMaxBytes {
		return printErr("Plan exceeds size limit", fmt.Errorf("recovery plan is too large"))
	}
	if err = json.Unmarshal(raw, &plan); err != nil {
		return printErr("Invalid recovery plan", err)
	}
	if plan.Report.SourceID != positional[0] {
		return printErr("Plan source mismatch", fmt.Errorf("use the original source UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	out, err := client.ApplyCommitRecovery(ctx, plan, *limit, *pages)
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	if out.Counts["unknown"] > 0 || out.Counts["read_error"] > 0 || out.Counts["rejected"] > 0 || out.Counts["not_attempted"] > 0 {
		return 1
	}
	return 0
}
