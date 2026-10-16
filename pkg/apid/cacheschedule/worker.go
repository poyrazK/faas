// Package cacheschedule executes customer-authorized one-time cache intent in apid.
package cacheschedule

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// RunOne claims one due action. A lost reply or process crash leaves a renewable
// lease; replay uses the same action UUID and recovers its original receipt.
func RunOne(ctx context.Context, s state.EnvironmentCacheScheduleWorker) (bool, error) {
	c, err := s.ClaimEnvironmentCacheSchedule(ctx)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	out, recovered, err := s.RecoverEnvironmentCacheScheduleAction(ctx, c)
	if err == nil && recovered {
		out.Status = "executed"
		return true, s.FinishEnvironmentCacheSchedule(ctx, c, out)
	}
	if err != nil {
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrInvalidArgument) {
			out.Status = "failed"
			out.ErrorCode = "action_rejected"
			return true, s.FinishEnvironmentCacheSchedule(ctx, c, out)
		}
		return true, err
	}
	if p := out.Policy; p != nil {
		result, e := s.SetEnvironmentCachePolicy(ctx, c.AccountID, out.AppID, out.Scope, *p)
		err = e
		if e == nil {
			out.PolicyResult = &result
		}
	} else if p := out.Purge; p != nil {
		result, e := s.CreateEnvironmentCachePurge(ctx, c.AccountID, out.AppID, out.Scope, *p)
		err = e
		if e == nil {
			out.PurgeResult = &result
		}
	} else {
		err = state.ErrInvalidArgument
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrRouteHealthRevision):
			out.ErrorCode = "policy_or_target_changed"
		case errors.Is(err, state.ErrNotFound):
			out.ErrorCode = "target_not_found"
		case errors.Is(err, state.ErrEnvironmentCacheAccount):
			out.ErrorCode = "account_ineligible"
		case errors.Is(err, state.ErrInvalidArgument), errors.Is(err, state.ErrConflict):
			out.ErrorCode = "action_rejected"
		default:
			return true, err // lease expiry retries transient/ambiguous failures
		}
		out.Status = "failed"
	} else {
		out.Status = "executed"
	}
	return true, s.FinishEnvironmentCacheSchedule(ctx, c, out)
}
func Run(ctx context.Context, s state.EnvironmentCacheScheduleWorker, log *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for i := 0; i < 10; i++ {
			actionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			worked, err := RunOne(actionCtx, s)
			cancel()
			if err != nil {
				log.WarnContext(ctx, "environment cache schedule execution failed", "error", err)
				break
			}
			if !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
