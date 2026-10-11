package main

import (
	"context"
	"encoding/json"
	"errors"
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
	checkpointPath := fs.String("checkpoint", "", "atomically save watch progress to this status JSON file")
	diagnostics := fs.Bool("diagnostics", false, "include bounded live delivery blocker diagnostics")
	diagnosticAge := fs.Duration("max-diagnostic-age", 0, "maximum diagnostic sample age (100ms-24h; requires diagnostics; default disabled)")
	maxAge := fs.Duration("max-observation-age", 0, "maximum watch evidence age (100ms-24h; default disabled)")
	readRetries := fs.Int("read-retries", 0, "total transient watch read retries (0..20; default 0)")
	watchDiagnostics := fs.Bool("watch-diagnostics", false, "watch until diagnostic coverage is complete and fresh; enables watch and diagnostics")
	watch := fs.Bool("watch", false, "monitor acknowledged recovery executions until terminal or timeout")
	interval := fs.Duration("interval", api.CommitRecoveryWatchIntervalDefault, "watch polling interval (100ms-1m)")
	refreshPath := fs.String("refresh", "", "saved recovery status; reread selection and track changes")
	limit := fs.Int("limit", api.CommitRecoveryStatusMax, "subscriber observations")
	pages := fs.Int("recipient-pages", 1, "receipt pages per item")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *watchDiagnostics {
		*watch = true
		*diagnostics = true
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *planPath == "" || *resultPath == "" || *limit < 1 || *limit > api.CommitRecoveryStatusMax || *pages < 1 || *pages > api.CommitDeliveryReportPagesMax || *timeout <= 0 || *timeout > 24*time.Hour || (*resumePath != "" && *refreshPath != "") || *interval < api.CommitRecoveryWatchIntervalMin || *interval > api.CommitRecoveryWatchIntervalMax || (*checkpointPath != "" && !*watch) || *readRetries < 0 || *readRetries > api.CommitRecoveryWatchReadRetriesMax || (*readRetries != 0 && !*watch) || *diagnosticAge < 0 || *diagnosticAge > api.CommitRecoveryDiagnosticAgeMax || (*diagnosticAge > 0 && (*diagnosticAge < api.CommitRecoveryDiagnosticAgeMin || !*diagnostics)) || *maxAge < 0 || *maxAge > api.CommitRecoveryWatchObservationAgeMax || (*maxAge > 0 && (*maxAge < api.CommitRecoveryWatchObservationAgeMin || !*watch)) {
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
	var checkpoint *commitRecoveryCheckpoint
	if *checkpointPath != "" {
		var checkpointErr error
		checkpoint, checkpointErr = newCommitRecoveryCheckpoint(*checkpointPath, *planPath, *resultPath)
		if checkpointErr != nil {
			return printErr("Cannot use recovery checkpoint", checkpointErr)
		}
		if checkpointErr = checkpoint.lock(*resumePath, *refreshPath); checkpointErr != nil {
			return printErr("Cannot own recovery checkpoint", checkpointErr)
		}
		defer func() { _ = checkpoint.close() }()
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
	var checkpointErr error
	progressCalled := false
	if *watch {
		last := ""
		lastRows := map[string]string{}
		lastDiagnostics := map[string]string{}
		lastEstimateWarning := false
		progress := func(status api.CommitRecoveryStatusResult) {
			progressCalled = true
			if checkpoint != nil {
				if checkpointErr = checkpoint.save(status); checkpointErr != nil {
					cancel()
					return
				}
			}
			printCommitRecoveryDiagnosticChanges(status, lastDiagnostics)
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
			terminal := api.CommitRecoveryStatusSettled(status)
			var freshness map[string]int
			if status.Monitoring != nil {
				terminal = status.Monitoring.AcknowledgedExecutionsTerminal
				freshness = status.Monitoring.Freshness
			}
			raw, _ := json.Marshal([]any{rows, freshness, commitRecoveryDiagnosticSummary(status.DiagnosticsProgress), commitRecoveryEstimateSummary(status.Monitoring)})
			if string(raw) != last {
				last = string(raw)
				_, _ = fmt.Fprintf(os.Stderr, "Recovery status: %d remaining; counts=%v; acknowledged executions terminal=%t\n", status.Remaining, status.Counts, terminal)
				if status.DiagnosticsProgress != nil {
					d := status.DiagnosticsProgress
					_, _ = fmt.Fprintf(os.Stderr, "Recovery diagnostics: %d remaining; counts=%v; retained=%d; deferred=%d; freshness=%v; compared=%d; changed=%d\n", d.Remaining, d.Counts, d.Retained, d.Deferred, d.Freshness, d.Compared, d.Changed)
				}
				if status.Monitoring != nil && status.Monitoring.DiagnosticEstimate != nil {
					rawEstimate, _ := json.Marshal(status.Monitoring.DiagnosticEstimate)
					_, _ = fmt.Fprintf(os.Stderr, "Diagnostic coverage estimate: %s\n", rawEstimate)
				}
				if freshness != nil {
					_, _ = fmt.Fprintf(os.Stderr, "Recovery freshness: %v\n", freshness)
				}
			}
			warning := status.Monitoring != nil && status.Monitoring.DiagnosticEstimate != nil && status.Monitoring.DiagnosticEstimate.RefreshExceedsMaxAge
			if warning && !lastEstimateWarning {
				_, _ = fmt.Fprintln(os.Stderr, "Diagnostic refresh estimate exceeds the configured age limit; this is advisory and does not predict fresh coverage completion.")
			}
			lastEstimateWarning = warning
		}
		out, err = client.WatchCommitRecoveryStatusWithOptions(ctx, plan, result, saved, *refreshPath != "", *limit, *pages, *interval, progress, api.CommitRecoveryWatchOptions{ReadRetries: *readRetries, MaxObservationAge: *maxAge, Diagnostics: *diagnostics, WatchDiagnostics: *watchDiagnostics, MaxDiagnosticAge: *diagnosticAge})
	}
	if !*watch && err == nil && *diagnostics {
		if saved != nil && *resumePath != "" {
			out, err = client.DiagnoseCommitRecoveryStatusWithOptions(ctx, out, saved, api.CommitRecoveryDiagnosticsOptions{MaxDiagnosticAge: *diagnosticAge})
		} else {
			out, err = client.DiagnoseCommitRecoveryStatusWithOptions(ctx, out, nil, api.CommitRecoveryDiagnosticsOptions{MaxDiagnosticAge: *diagnosticAge})
		}
	}
	var validation *api.Problem
	invalidSelection := errors.As(err, &validation) && validation.Code == api.CodeValidation
	if checkpoint != nil && checkpointErr == nil && !invalidSelection {
		meaningful := progressCalled
		for _, row := range out.Items {
			meaningful = meaningful || row.ObservedAt != nil || row.Status == "read_error"
		}
		if meaningful {
			checkpointErr = checkpoint.save(out)
		}
	}
	checkpointCode := 0
	if checkpointErr != nil {
		if out.Monitoring != nil {
			out.Monitoring.StopReason = "checkpoint_failure"
		}
		checkpointCode = printErr("Cannot save recovery checkpoint", checkpointErr)
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if checkpointCode != 0 {
		return checkpointCode
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	if *watchDiagnostics {
		if out.Monitoring != nil && out.Monitoring.StopReason == "diagnostic_coverage_complete" && out.Monitoring.DiagnosticCoverageComplete {
			return 0
		}
		return 1
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

func commitRecoveryDiagnosticSummary(progress *api.CommitRecoveryDiagnosticProgress) any {
	if progress == nil {
		return nil
	}
	return []any{progress.Counts, progress.Remaining, progress.Deferred, progress.Retained, progress.Freshness, progress.FreshComplete, progress.Compared, progress.Changed}
}

func printCommitRecoveryDiagnosticChanges(status api.CommitRecoveryStatusResult, seen map[string]string) {
	for _, row := range status.Items {
		diagnostic := row.Diagnostics
		if diagnostic == nil || diagnostic.Retained || diagnostic.PreviousObservation == nil || len(diagnostic.Changes) == 0 {
			continue
		}
		identity, _ := json.Marshal(diagnostic.PreviousObservation.Query)
		pair, _ := json.Marshal([]any{diagnostic.PreviousObservation.ObservedAt, diagnostic.ObservedAt, diagnostic.Changes})
		if seen[string(identity)] == string(pair) {
			continue
		}
		seen[string(identity)] = string(pair)
		_, _ = fmt.Fprintf(os.Stderr, "Recovery routing changes %s: [previous observation, current observation, changes]=%s\n", identity, pair)
	}
}

func commitRecoveryEstimateSummary(monitoring *api.CommitRecoveryMonitoring) any {
	if monitoring == nil || monitoring.DiagnosticEstimate == nil {
		return nil
	}
	return []any{monitoring.DiagnosticEstimate.State, monitoring.DiagnosticEstimate.RefreshExceedsMaxAge}
}
