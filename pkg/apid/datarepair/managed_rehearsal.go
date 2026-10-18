package datarepair

import (
	"context"

	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Managed) enqueueRehearsal(ctx context.Context, op state.Operation, b Binding) (state.Operation, core.Plan, error) {
	tasks, ok := s.Host.Store().(state.DataRepairRehearsalStore)
	if !ok {
		return op, core.Plan{}, core.ErrUnavailable
	}
	if _, ok := b.Executor.(RehearsalExecutor); !ok {
		return op, core.Plan{}, core.ErrUnavailable
	}
	p, err := b.Executor.Get(ctx, b.Scope, op.DataRepair.PlanID)
	if err != nil {
		return op, p, err
	}
	if !matchesPlan(op, p) || p.Candidates < 1 || p.ApprovedAt != nil {
		return op, p, core.ErrConflict
	}
	op, _, err = tasks.EnqueueDataRepairRehearsal(ctx, op.AccountID, op.ID, op.LatestSequence, p.ChecksSHA256)
	if err == nil {
		s.audit(ctx, op, "rehearsal_queued")
	}
	return op, p, err
}

func (s *Managed) cancelRehearsal(ctx context.Context, op state.Operation, b Binding) (state.Operation, core.Plan, error) {
	tasks, ok := s.Host.Store().(state.DataRepairRehearsalStore)
	if !ok || !b.ScheduledRehearsal {
		return op, core.Plan{}, core.ErrUnavailable
	}
	op, err := tasks.CancelDataRepairRehearsal(ctx, op.AccountID, op.ID, op.LatestSequence)
	if err != nil {
		return op, core.Plan{}, err
	}
	s.audit(ctx, op, "rehearsal_cancel_requested")
	// Source availability must not prevent durable cancellation. A failed
	// response refresh leaves the recorded intent for schedd to retire.
	p, err := b.Executor.Get(ctx, b.Scope, op.DataRepair.PlanID)
	return op, p, err
}
