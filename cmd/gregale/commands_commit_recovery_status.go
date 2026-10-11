package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdCommitRecoveryStatus(args []string) int {
	fs := newFlagSet("commit recovery-status", flag.ContinueOnError)
	planPath := fs.String("plan", "", "reviewed recovery plan JSON")
	resultPath := fs.String("result", "", "saved recovery apply result JSON")
	limit := fs.Int("limit", api.CommitRecoveryStatusMax, "subscriber observations")
	pages := fs.Int("recipient-pages", 1, "receipt pages per item")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *planPath == "" || *resultPath == "" || *limit < 1 || *limit > api.CommitRecoveryStatusMax || *pages < 1 || *pages > api.CommitDeliveryReportPagesMax || *timeout <= 0 || *timeout > 24*time.Hour {
		return 1
	}
	var plan api.CommitRecoveryPlan
	var result api.CommitRecoveryApplyResult
	for _, input := range []struct {
		path   string
		target any
	}{{*planPath, &plan}, {*resultPath, &result}} {
		if err := readCommitRecoveryStatusFile(input.path, input.target); err != nil {
			return printErr("Cannot read recovery status input", err)
		}
	}
	if plan.Report.SourceID != positional[0] || result.SourceID != positional[0] {
		return printErr("Recovery source mismatch", fmt.Errorf("use the original source UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	out, err := client.GetCommitRecoveryStatus(ctx, plan, result, *limit, *pages)
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	if !out.Complete || out.Counts["unresolved"] > 0 || out.Counts["failed"] > 0 || out.Counts["unsupported_recipient"] > 0 {
		return 1
	}
	for _, row := range out.Items {
		if row.Attempt.Status == "unknown" {
			return 1
		}
	}
	return 0
}

func readCommitRecoveryStatusFile(path string, target any) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.CommitDeliveryReportFileMaxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > api.CommitDeliveryReportFileMaxBytes {
		return fmt.Errorf("recovery status input exceeds size limit")
	}
	return json.Unmarshal(raw, target)
}
