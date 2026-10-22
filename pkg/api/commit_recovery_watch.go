package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// WatchCommitRecoveryStatus polls a fixed selection within the caller's deadline.
// Progress receives detached snapshots; unknown attempts never prove recovery.
func (c *Client) WatchCommitRecoveryStatus(ctx context.Context, plan CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult, refresh bool, limit, pages int, interval time.Duration, progress func(CommitRecoveryStatusResult)) (CommitRecoveryStatusResult, error) {
	return c.WatchCommitRecoveryStatusWithRetry(ctx, plan, applied, previous, refresh, limit, pages, interval, progress, 0)
}

// CommitRecoveryWatchOptions controls optional retry and evidence-age policies.
type CommitRecoveryWatchOptions struct {
	DurableAlerts           bool
	AlertAcknowledgements   []CommitRecoveryAlertAcknowledgement
	OnAlertAcknowledgements func(CommitRecoveryStatusResult) ([]CommitRecoveryAlertAcknowledgement, error)
	StallThreshold          time.Duration
	WatchDiagnostics        bool
	MaxDiagnosticAge        time.Duration
	Diagnostics             bool
	ReadRetries             int
	MaxObservationAge       time.Duration
}

// WatchCommitRecoveryStatusWithRetry permits a bounded total number of transient read retries.
func (c *Client) WatchCommitRecoveryStatusWithRetry(ctx context.Context, plan CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult, refresh bool, limit, pages int, interval time.Duration, progress func(CommitRecoveryStatusResult), readRetries int) (CommitRecoveryStatusResult, error) {
	return c.WatchCommitRecoveryStatusWithOptions(ctx, plan, applied, previous, refresh, limit, pages, interval, progress, CommitRecoveryWatchOptions{ReadRetries: readRetries})
}

// WatchCommitRecoveryStatusWithOptions enforces optional evidence freshness before settlement.
func (c *Client) WatchCommitRecoveryStatusWithOptions(ctx context.Context, plan CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult, refresh bool, limit, pages int, interval time.Duration, progress func(CommitRecoveryStatusResult), options CommitRecoveryWatchOptions) (out CommitRecoveryStatusResult, err error) {
	if options.WatchDiagnostics {
		options.Diagnostics = true
	}
	readRetries, maxAge := options.ReadRetries, options.MaxObservationAge
	started := time.Now()
	passes, usedRetries := 0, 0
	reason := "read_failure"
	estimator := &commitRecoveryDiagnosticEstimator{}
	stalls := &commitRecoveryStallTracker{}
	incidents := newCommitRecoveryIncidentTracker()
	incidents.durable = options.DurableAlerts
	alertsRestored := false
	estimateDelay := interval
	summary := func(stopReason string) *CommitRecoveryMonitoring {
		freshness, fresh := commitRecoveryFreshnessCounts(out, maxAge)
		mode := "recovery_settlement"
		if options.WatchDiagnostics {
			mode = "diagnostic_coverage"
		}
		coverage := false
		if stopReason == "diagnostic_coverage_complete" && out.DiagnosticsProgress != nil {
			coverage = out.DiagnosticsProgress.Complete && out.DiagnosticsProgress.FreshComplete
		} else {
			coverage = commitRecoveryDiagnosticCoverageComplete(out, options.MaxDiagnosticAge)
		}
		consumers := stalls.summarize(out, options.MaxDiagnosticAge, options.StallThreshold)
		active, transitions := incidents.summarize(out, consumers, options.MaxDiagnosticAge, options.StallThreshold)
		outbox := incidents.outbox(active)
		if !alertsRestored {
			outbox = nil
		}
		return &CommitRecoveryMonitoring{AlertOutbox: outbox, StallThresholdMS: options.StallThreshold.Milliseconds(), ConsumerStalls: consumers, BlockerIncidents: active, BlockerTransitions: transitions, DiagnosticEstimate: estimator.estimate(out, time.Since(started).Seconds()*1000, estimateDelay.Seconds()*1000), WatchMode: mode, DiagnosticCoverageComplete: coverage, StopReason: stopReason, Passes: passes, RetriesConsumed: usedRetries, ElapsedMS: time.Since(started).Milliseconds(), AcknowledgedExecutionsTerminal: CommitRecoveryStatusSettled(out) && fresh, MaxObservationAgeMS: maxAge.Milliseconds(), Freshness: freshness}
	}
	defer func() {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = "timeout"
		} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			reason = "cancelled"
		} else {
			var validation *Problem
			if errors.As(err, &validation) && validation.Code == CodeValidation && reason != "callback_failure" {
				reason = "validation_failure"
			}
		}
		out.Monitoring = summary(reason)
	}()
	if _, ok := ctx.Deadline(); !ok || interval < CommitRecoveryWatchIntervalMin || interval > CommitRecoveryWatchIntervalMax || readRetries < 0 || readRetries > CommitRecoveryWatchReadRetriesMax || maxAge < 0 || maxAge > CommitRecoveryWatchObservationAgeMax || (maxAge > 0 && maxAge < CommitRecoveryWatchObservationAgeMin) {
		return out, ErrValidation("watch requires a deadline and a valid polling interval")
	}
	if ageErr := validateDiagnosticAge(options.MaxDiagnosticAge); ageErr != nil {
		return out, ageErr
	}
	if options.MaxDiagnosticAge > 0 && !options.Diagnostics {
		return out, ErrValidation("maximum diagnostic age requires diagnostics")
	}
	if options.StallThreshold < 0 || options.StallThreshold > CommitRecoveryStallThresholdMax || (options.StallThreshold > 0 && (options.StallThreshold < CommitRecoveryStallThresholdMin || !options.Diagnostics || options.MaxDiagnosticAge == 0)) {
		return out, ErrValidation("stall threshold requires diagnostics and a positive diagnostic age limit (100ms-24h)")
	}
	if len(options.AlertAcknowledgements) > CommitRecoveryAlertQueueMax || (options.DurableAlerts && options.StallThreshold == 0) || (!options.DurableAlerts && (len(options.AlertAcknowledgements) > 0 || options.OnAlertAcknowledgements != nil)) {
		return out, ErrValidation("durable alerts require stall detection; acknowledgements require durable alerts")
	}
	// Freeze caller-owned inputs across polling passes and progress callbacks.
	saved, err := cloneCommitRecoveryWatchInputs(plan, applied, previous)
	if err != nil {
		return out, err
	}
	plan, applied, previous = saved.Plan, saved.Applied, saved.Previous
	alertPrevious := previous
	initialAcks := append([]CommitRecoveryAlertAcknowledgement{}, options.AlertAcknowledgements...)
	diagnosticPrevious := previous
	if refresh {
		diagnosticPrevious = nil
	}
	if previous != nil && !refresh {
		refresh = true
		for _, row := range previous.Items {
			if row.Status == "not_observed" || row.Status == "read_error" {
				refresh = false
				break
			}
		}
	}
	for {
		incidents.beginPass()
		passes++
		out, err = c.getCommitRecoveryStatusWithAge(ctx, plan, applied, previous, refresh, limit, pages, maxAge)
		var validation *Problem
		if !alertsRestored && (!errors.As(err, &validation) || validation.Code != CodeValidation) && (err == nil || len(out.Items) > 0) {
			if restoreErr := incidents.restore(alertPrevious, out, initialAcks); restoreErr != nil {
				return out, restoreErr
			}
			alertsRestored = true
		}
		if options.Diagnostics && (err == nil || len(out.Items) > 0) {
			diagnosticCtx := ctx
			if err != nil {
				paused, stop := context.WithCancel(ctx)
				stop()
				diagnosticCtx = paused
			}
			diagnosticStarted := time.Now()
			diagnosed, diagnosticErr := c.diagnoseCommitRecoveryStatus(diagnosticCtx, out, diagnosticPrevious, options.MaxDiagnosticAge, options.WatchDiagnostics)
			if diagnosticErr == nil || diagnosed.DiagnosticsProgress != nil {
				out = diagnosed
			}
			estimator.observe(out, time.Since(diagnosticStarted).Seconds()*1000)
			stalls.observe(out, options.MaxDiagnosticAge, options.StallThreshold)
			if err == nil {
				err = diagnosticErr
			}
		}
		delay := interval
		if err != nil {
			retryAfter, retryable := commitRecoveryReadRetry(err)
			if !retryable || usedRetries >= readRetries || ctx.Err() != nil {
				if retryable && readRetries > 0 && usedRetries >= readRetries {
					reason = "retry_exhausted"
				}
				return out, err
			}
			usedRetries++
			delay = interval
			for n := 1; n < usedRetries && delay < CommitRecoveryWatchIntervalMax; n++ {
				delay *= 2
			}
			if delay > CommitRecoveryWatchIntervalMax {
				delay = CommitRecoveryWatchIntervalMax
			}
			if retryAfter > delay {
				delay = retryAfter
			}
		}
		estimateDelay = delay
		out.Monitoring = summary("running")
		if progress != nil {
			snapshot, copyErr := cloneCommitRecoveryWatchInputs(plan, applied, &out)
			if copyErr != nil {
				return out, copyErr
			}
			progress(*snapshot.Previous)
		}
		if options.OnAlertAcknowledgements != nil {
			snapshot, copyErr := cloneCommitRecoveryWatchInputs(plan, applied, &out)
			if copyErr != nil {
				return out, copyErr
			}
			acknowledgements, ackErr := options.OnAlertAcknowledgements(*snapshot.Previous)
			if ackErr == nil {
				ackErr = incidents.acknowledge(acknowledgements)
			}
			if ackErr != nil {
				reason = "callback_failure"
				return out, ackErr
			}
		}
		if incidents.queueFull {
			reason = "alert_queue_full"
			return out, errors.New("alert queue full; acknowledge pending alerts and resume")
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		_, fresh := commitRecoveryFreshnessCounts(out, maxAge)
		if err == nil && options.WatchDiagnostics && commitRecoveryDiagnosticCoverageComplete(out, options.MaxDiagnosticAge) {
			reason = "diagnostic_coverage_complete"
			return out, nil
		}
		if err == nil && !options.WatchDiagnostics && CommitRecoveryStatusSettled(out) && fresh {
			reason = "settled"
			return out, nil
		}
		diagnosticSnapshot := out
		diagnosticPrevious = &diagnosticSnapshot
		previous = &out
		// Complete observation passes refresh; partial passes resume without
		// restarting at the first subscriber.
		refresh = err == nil && out.Remaining == 0 && fresh
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C:
		}
	}
}

// CommitRecoveryStatusSettled requires acknowledged terminal execution evidence
// for every queued attempt. Unknown or unobserved outcomes never settle a watch.
func CommitRecoveryStatusSettled(out CommitRecoveryStatusResult) bool {
	if !out.Complete || out.Remaining != 0 {
		return false
	}
	for _, row := range out.Items {
		switch row.Attempt.Status {
		case "queued":
			if (row.Status != "completed" && row.Status != "failed") || row.Attribution != "matches_acknowledged_execution" || (row.ExecutionState != "completed" && row.ExecutionState != "failed" && row.ExecutionState != "cancelled" && row.ExecutionState != "expired" && row.ExecutionState != "superseded") {
				return false
			}
		case "rejected", "skipped", "not_attempted":
			// No acknowledged submission to monitor; these remain explicit in output.
		default:
			return false
		}
	}
	return true
}

type commitRecoveryWatchInputs struct {
	Plan     CommitRecoveryPlan
	Applied  CommitRecoveryApplyResult
	Previous *CommitRecoveryStatusResult
}

func cloneCommitRecoveryWatchInputs(plan CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult) (commitRecoveryWatchInputs, error) {
	var out commitRecoveryWatchInputs
	raw, err := json.Marshal(commitRecoveryWatchInputs{Plan: plan, Applied: applied, Previous: previous})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

// Every error in a partial pass must be transient; never hide a permanent failure.
func commitRecoveryReadRetry(err error) (time.Duration, bool) {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var longest time.Duration
		for _, cause := range joined.Unwrap() {
			delay, retryable := commitRecoveryReadRetry(cause)
			if !retryable {
				return 0, false
			}
			if delay > longest {
				longest = delay
			}
		}
		return longest, true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Problem.Status {
		case 429, 500, 502, 503, 504:
		default:
			return 0, false
		}
		var delay time.Duration
		if seconds := apiErr.Problem.RetryAfterSeconds; seconds != nil && *seconds > 0 {
			// A longer server delay waits for the caller's bounded deadline instead.
			if *seconds > int64((1<<63-1)/time.Second) {
				delay = time.Duration(1<<63 - 1)
			} else {
				delay = time.Duration(*seconds) * time.Second
			}
		}
		for _, value := range apiErr.Problem.HasHeader("Retry-After") {
			if seconds, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil && seconds > 0 {
				if seconds > int64((1<<63-1)/time.Second) {
					seconds = int64((1<<63 - 1) / time.Second)
				}
				if candidate := time.Duration(seconds) * time.Second; candidate > delay {
					delay = candidate
				}
			} else if date, parseErr := http.ParseTime(value); parseErr == nil {
				if candidate := time.Until(date); candidate > delay {
					delay = candidate
				}
			}
		}
		return delay, true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, true
	}
	var network net.Error
	return 0, errors.As(err, &network)
}

func commitRecoveryFreshness(row CommitRecoveryStatusItem, now time.Time, maxAge time.Duration) string {
	if row.Status == "not_observed" || row.Status == "read_error" {
		return "unobserved"
	}
	if row.ObservedAt == nil || row.ObservedAt.IsZero() {
		return "missing_timestamp"
	}
	if row.ObservedAt.After(now) {
		return "future_timestamp"
	}
	if now.Sub(*row.ObservedAt) > maxAge {
		return "stale"
	}
	return "fresh"
}

func commitRecoveryFreshnessCounts(out CommitRecoveryStatusResult, maxAge time.Duration) (map[string]int, bool) {
	if maxAge == 0 {
		return nil, true
	}
	counts := map[string]int{"fresh": 0, "stale": 0, "missing_timestamp": 0, "future_timestamp": 0, "unobserved": 0}
	now := time.Now()
	for _, row := range out.Items {
		counts[commitRecoveryFreshness(row, now, maxAge)]++
	}
	return counts, counts["fresh"] == len(out.Items)
}
