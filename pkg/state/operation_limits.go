// adr: 1196
package state

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dispatch"
)

// applyOperationLimits fixes a definition's limits onto a new Operation. The
// HTTP execution inherits the deadline as its invocation deadline, so the
// existing claim fence and claim context enforce it, and max_attempts as its
// retry policy, which the drain bounds by the plan ceiling.
func applyOperationLimits(op *Operation, inv *Invocation, limits *api.OperationLimits, now time.Time) error {
	if limits == nil {
		return nil
	}
	l := *limits
	op.Limits, op.Usage = &l, &api.OperationUsage{}
	if l.DeadlineSeconds > 0 {
		at := now.Add(time.Duration(l.DeadlineSeconds) * time.Second)
		op.DeadlineAt = &at
		if inv.DeadlineAt == nil || at.Before(*inv.DeadlineAt) {
			inv.DeadlineAt = &at
		}
	}
	if l.MaxAttempts > 0 {
		raw, err := json.Marshal(dispatch.RetryPolicy{MaxAttempts: l.MaxAttempts})
		if err != nil {
			return err
		}
		inv.RetryPolicyJSON = raw
	}
	return nil
}

// operationLimitExceeded reports which limit, if any, an Operation has used up.
func operationLimitExceeded(op Operation, now time.Time) string {
	if op.DeadlineAt != nil && !now.Before(*op.DeadlineAt) {
		return api.OperationFailureDeadlineExceeded
	}
	if op.Limits != nil && op.Limits.MaxExecutionSeconds > 0 && op.Usage != nil &&
		op.Usage.ExecutionMS >= int64(op.Limits.MaxExecutionSeconds)*1000 {
		return api.OperationFailureBudgetExhausted
	}
	return ""
}

// recordOperationAttemptUsage adds a finished attempt to an Operation with
// limits. A running Operation has exactly one claimed attempt, timed from its
// claim; every transition out of running ends it.
func recordOperationAttemptUsage(op *Operation, inv Invocation, now time.Time) {
	if op.Usage == nil || op.State != api.OperationRunning || inv.ReceivedAt == nil {
		return
	}
	op.Usage.ExecutionMS += max(now.Sub(*inv.ReceivedAt).Milliseconds(), 0)
	op.Usage.Attempts++
}

// operationRetryLimitExceeded reports the limit that forbids a pending retry:
// a spent budget, or a deadline reached before the retry is due.
func operationRetryLimitExceeded(op Operation, inv Invocation, now time.Time) string {
	if code := operationLimitExceeded(op, now); code != "" {
		return code
	}
	if op.DeadlineAt != nil && !inv.DueAt.Before(*op.DeadlineAt) {
		return api.OperationFailureDeadlineExceeded
	}
	return ""
}

// operationRetryCancelled reports that a transition settled the Operation
// while its execution waits to retry; the store must cancel that execution.
func operationRetryCancelled(op Operation, inv Invocation) bool {
	return inv.State == InvocationPending && op.State == api.OperationFailed
}

// operationClaimDeadline caps a claimed attempt at the remaining execution
// budget; the drain's claim context ends the attempt there.
func operationClaimDeadline(op Operation, inv Invocation, now time.Time) *time.Time {
	remaining, ok := operationRemainingExecution(op)
	if !ok {
		return inv.DeadlineAt
	}
	at := now.Add(remaining)
	if inv.DeadlineAt != nil && inv.DeadlineAt.Before(at) {
		return inv.DeadlineAt
	}
	return &at
}

// operationRemainingExecution is the runtime an attempt may still use, or
// zero when the Operation has no execution budget.
func operationRemainingExecution(op Operation) (time.Duration, bool) {
	if op.Limits == nil || op.Limits.MaxExecutionSeconds == 0 {
		return 0, false
	}
	used := int64(0)
	if op.Usage != nil {
		used = op.Usage.ExecutionMS
	}
	return time.Duration(int64(op.Limits.MaxExecutionSeconds)*1000-used) * time.Millisecond, true
}

func operationPGTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
