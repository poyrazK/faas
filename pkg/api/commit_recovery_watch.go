package api

import (
	"context"
	"encoding/json"
	"time"
)

// WatchCommitRecoveryStatus polls a fixed selection within the caller's deadline.
// Progress receives detached snapshots; unknown attempts never prove recovery.
func (c *Client) WatchCommitRecoveryStatus(ctx context.Context, plan CommitRecoveryPlan, applied CommitRecoveryApplyResult, previous *CommitRecoveryStatusResult, refresh bool, limit, pages int, interval time.Duration, progress func(CommitRecoveryStatusResult)) (CommitRecoveryStatusResult, error) {
	var out CommitRecoveryStatusResult
	if _, ok := ctx.Deadline(); !ok || interval < CommitRecoveryWatchIntervalMin || interval > CommitRecoveryWatchIntervalMax {
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
		out, err = c.getCommitRecoveryStatus(ctx, plan, applied, previous, refresh, limit, pages)
		if err != nil {
			return out, err
		}
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
		if CommitRecoveryStatusSettled(out) {
			return out, nil
		}
		previous = &out
		// Complete observation passes refresh; partial passes resume without
		// restarting at the first subscriber.
		refresh = out.Remaining == 0
		timer := time.NewTimer(interval)
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
