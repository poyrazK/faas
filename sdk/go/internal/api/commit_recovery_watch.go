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

// WatchCommitRecoveryStatusWithRetry permits a bounded total number of transient read retries.
func (c *Client) WatchCommitRecoveryStatusWithRetry(ctx context.Context, plan CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult, refresh bool, limit, pages int, interval time.Duration, progress func(CommitRecoveryStatusResult), readRetries int) (out CommitRecoveryStatusResult, err error) {
	started := time.Now()
	passes, usedRetries := 0, 0
	reason := "read_failure"
	summary := func(stopReason string) *CommitRecoveryMonitoring {
		return &CommitRecoveryMonitoring{StopReason: stopReason, Passes: passes, RetriesConsumed: usedRetries, ElapsedMS: time.Since(started).Milliseconds(), AcknowledgedExecutionsTerminal: CommitRecoveryStatusSettled(out)}
	}
	defer func() {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = "timeout"
		} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			reason = "cancelled"
		} else {
			var validation *Problem
			if errors.As(err, &validation) && validation.Code == CodeValidation {
				reason = "validation_failure"
			}
		}
		out.Monitoring = summary(reason)
	}()
	if _, ok := ctx.Deadline(); !ok || interval < CommitRecoveryWatchIntervalMin || interval > CommitRecoveryWatchIntervalMax || readRetries < 0 || readRetries > CommitRecoveryWatchReadRetriesMax {
		return out, ErrValidation("watch requires a deadline and a valid polling interval")
	}
	// Freeze caller-owned inputs across polling passes and progress callbacks.
	saved, err := cloneCommitRecoveryWatchInputs(plan, applied, previous)
	if err != nil {
		return out, err
	}
	plan, applied, previous = saved.Plan, saved.Applied, saved.Previous
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
		passes++
		out, err = c.getCommitRecoveryStatus(ctx, plan, applied, previous, refresh, limit, pages)
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
		out.Monitoring = summary("running")
		if progress != nil {
			snapshot, copyErr := cloneCommitRecoveryWatchInputs(plan, applied, &out)
			if copyErr != nil {
				return out, copyErr
			}
			progress(*snapshot.Previous)
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if err == nil && CommitRecoveryStatusSettled(out) {
			reason = "settled"
			return out, nil
		}
		previous = &out
		// Complete observation passes refresh; partial passes resume without
		// restarting at the first subscriber.
		refresh = err == nil && out.Remaining == 0
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
		for _, value := range []string{apiErr.Problem.RetryAfter} {
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

const (
	CommitRecoveryWatchIntervalMin    = 100 * time.Millisecond
	CommitRecoveryWatchIntervalMax    = time.Minute
	CommitRecoveryWatchReadRetriesMax = 20
)
