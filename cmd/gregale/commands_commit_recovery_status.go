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
	resumePath := fs.String("resume", "", "saved recovery status; continue unread items and failed reads")
	watch := fs.Bool("watch", false, "monitor acknowledged recovery executions until terminal or timeout")
	interval := fs.Duration("interval", api.CommitRecoveryWatchIntervalDefault, "watch polling interval (100ms-1m)")
	refreshPath := fs.String("refresh", "", "saved recovery status; reread selection and track changes")
	limit := fs.Int("limit", api.CommitRecoveryStatusMax, "subscriber observations")
	pages := fs.Int("recipient-pages", 1, "receipt pages per item")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *planPath == "" || *resultPath == "" || *limit < 1 || *limit > api.CommitRecoveryStatusMax || *pages < 1 || *pages > api.CommitDeliveryReportPagesMax || *timeout <= 0 || *timeout > 24*time.Hour || (*resumePath != "" && *refreshPath != "") || *interval < api.CommitRecoveryWatchIntervalMin || *interval > api.CommitRecoveryWatchIntervalMax {
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
	var out api.CommitRecoveryStatusResult
	var saved *api.CommitRecoveryStatusResult
	if *resumePath != "" || *refreshPath != "" {
		statusPath := *resumePath
		if *refreshPath != "" {
			statusPath = *refreshPath
		}
		var previous api.CommitRecoveryStatusResult
		if readErr := readCommitRecoveryStatusFile(statusPath, &previous); readErr != nil {
			return printErr("Cannot read saved recovery status", readErr)
		}
		saved = &previous
		if !*watch && *refreshPath != "" {
			out, err = client.RefreshCommitRecoveryStatus(ctx, plan, result, previous, *limit, *pages)
		} else if !*watch {
			out, err = client.ResumeCommitRecoveryStatus(ctx, plan, result, previous, *limit, *pages)
		}
	} else if !*watch {
		out, err = client.GetCommitRecoveryStatus(ctx, plan, result, *limit, *pages)
	}
	if *watch {
		last := ""
		lastRows := map[string]string{}
		progress := func(status api.CommitRecoveryStatusResult) {
			rows := make([]any, 0, len(status.Items))
			for _, row := range status.Items {
				fields := []any{row.Status, row.Reason, row.Generation, row.GenerationRelation, row.Attribution, row.ExecutionID, row.ExecutionState}
				rows = append(rows, fields)
				rawRow, _ := json.Marshal(fields)
				key := row.Attempt.EventID + ":" + row.Attempt.SubscriptionID
				if row.Status != "not_observed" && lastRows[key] != string(rawRow) {
					_, _ = fmt.Fprintf(os.Stderr, "Recovery %s: [status, reason, generation, relation, attribution, execution, state]=%s\n", key, rawRow)
				}
				lastRows[key] = string(rawRow)
			}
			raw, _ := json.Marshal(rows)
			if string(raw) != last {
				last = string(raw)
				_, _ = fmt.Fprintf(os.Stderr, "Recovery status: %d remaining; counts=%v; acknowledged executions terminal=%t\n", status.Remaining, status.Counts, api.CommitRecoveryStatusSettled(status))
			}
		}
		out, err = client.WatchCommitRecoveryStatus(ctx, plan, result, saved, *refreshPath != "", *limit, *pages, *interval, progress)
	}
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
		if row.Attempt.Status == "unknown" || (*watch && (row.Attempt.Status == "rejected" || row.Attempt.Status == "not_attempted")) {
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
