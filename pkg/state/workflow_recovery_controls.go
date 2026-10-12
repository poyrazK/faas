package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type WorkflowRecoveryPolicyOperatorStore interface {
	ControlWorkflowRecoveryPolicy(context.Context, string, string, api.ControlWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyResponse, error)
	GetWorkflowRecoveryAutomaticQueue(context.Context, string, string) (api.WorkflowRecoveryAutomaticQueue, error)
}

func recoveryQueueFinish(out *api.WorkflowRecoveryAutomaticQueue, last *time.Time) {
	out.RemainingBudget = out.Policy.Policy.HourlyExecutionBudget - out.HourlyExecutions
	if out.RemainingBudget < 0 {
		out.RemainingBudget = 0
	}
	if last != nil {
		until := last.Add(time.Duration(out.Policy.Policy.CooldownSeconds) * time.Second)
		if until.After(out.ObservedAt) {
			out.CooldownUntil = &until
		}
	}
	for i := range out.Entries {
		e := &out.Entries[i]
		e.QueuedAgeSeconds = recoveryQueueAge(out.ObservedAt, e.QueuedAt)
		if evaluation := e.Action.LastEvaluation; evaluation != nil && evaluation.WaitingSince != nil {
			age := recoveryQueueAge(out.ObservedAt, *evaluation.WaitingSince)
			e.WaitingAgeSeconds = &age
		}
	}
	out.Truncated = out.Pending+out.Outstanding > len(out.Entries)
}
func recoveryControlMatches(p api.WorkflowRecoveryPolicyResponse, r api.ControlWorkflowRecoveryPolicyRequest) bool {
	if p.Version != r.ExpectedVersion || !p.UpdatedAt.Equal(r.ExpectedUpdatedAt) || p.AutomaticExecution == nil {
		return false
	}
	a, b := p.AutomaticExecution.PausedAt, r.ExpectedPausedAt
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func recoveryQueueAge(now, since time.Time) int64 {
	if since.IsZero() || since.After(now) {
		return 0
	}
	return int64(now.Sub(since) / time.Second)
}
