package rollout

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Wait polls only read. The caller owns the overall deadline and cancellation;
// each request is bounded independently. Transient failures never reuse success.
func Wait(ctx context.Context, tracker *Tracker, read func(context.Context) (api.EnvironmentGitOpsStatusResponse, error), interval time.Duration, progress func(Result) error) (Result, error) {
	if tracker == nil || read == nil || interval <= 0 || interval > time.Minute {
		return Result{}, fmt.Errorf("invalid rollout wait configuration")
	}
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return Result{}, fmt.Errorf("rollout wait requires an overall deadline")
	}
	out := Result{ReadOnly: true, RequestedCommitSHA: tracker.commit, Status: "waiting", Reason: "requested_revision_unproven"}
	for ctx.Err() == nil && time.Now().Before(deadline) {
		pause := interval
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		status, err := read(readCtx)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && apiErr.Problem.Status >= 400 && apiErr.Problem.Status < 500 && apiErr.Problem.Status != 408 && apiErr.Problem.Status != 429 {
				return out, fmt.Errorf("GitOps status read failed (HTTP %d)", apiErr.Problem.Status)
			}
			if apiErr != nil {
				pause = retryAfterPause(apiErr.Problem, interval, time.Now(), deadline)
			}
			out = Result{ReadOnly: true, RequestedCommitSHA: tracker.commit, Status: "unavailable", Reason: "status_temporarily_unavailable"}
		} else {
			out, err = tracker.Observe(status, time.Now().UTC())
			if err != nil {
				return out, err
			}
		}
		if progress != nil {
			if err := progress(out); err != nil {
				return out, err
			}
		}
		if ctx.Err() != nil {
			break
		}
		if out.Status == "serving" || out.Status == "failed" || out.Status == "blocked" || out.Status == "superseded" {
			return out, nil
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	out.Status, out.Reason = "timed_out", "deadline_exceeded"
	if errors.Is(ctx.Err(), context.Canceled) {
		out.Status, out.Reason = "interrupted", "wait_canceled"
	}
	return out, nil
}

func retryAfterPause(problem api.Problem, interval time.Duration, now, deadline time.Time) time.Duration {
	pause, remaining := interval, max(time.Duration(0), deadline.Sub(now))
	addSeconds := func(seconds int64) {
		if seconds <= 0 {
			return
		}
		// Bound seconds before converting to Duration to avoid overflow.
		if seconds > int64(remaining/time.Second) {
			pause = max(pause, remaining)
		} else {
			pause = max(pause, time.Duration(seconds)*time.Second)
		}
	}
	if problem.RetryAfterSeconds != nil {
		addSeconds(*problem.RetryAfterSeconds)
	}
	for _, value := range problem.HasHeader("Retry-After") {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
			addSeconds(seconds)
		} else if at, err := http.ParseTime(value); err == nil {
			pause = max(pause, min(remaining, at.Sub(now)))
		}
	}
	return min(pause, remaining)
}
