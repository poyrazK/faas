package rollout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Wait polls only read. The caller owns the overall deadline and cancellation;
// each request is bounded independently. Transient failures never reuse success.
func Wait(ctx context.Context, tracker *Tracker, read func(context.Context) (api.EnvironmentGitOpsStatusResponse, error), interval time.Duration, progress func(Result) error) (Result, error) {
	if tracker == nil || read == nil || interval <= 0 || interval > time.Minute {
		return Result{}, fmt.Errorf("invalid rollout wait configuration")
	}
	if _, ok := ctx.Deadline(); !ok {
		return Result{}, fmt.Errorf("rollout wait requires an overall deadline")
	}
	out := Result{ReadOnly: true, RequestedCommitSHA: tracker.commit, Status: "waiting", Reason: "requested_revision_unproven"}
	for ctx.Err() == nil {
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
		timer := time.NewTimer(interval)
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
