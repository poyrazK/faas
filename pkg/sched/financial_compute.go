package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// The scheduler reflects a monetary hold through its existing instance owner.
// vmmd's local deadline is independently authoritative during control-plane loss.
func (e *Engine) reconcileStrictCompute(ctx context.Context, app state.App, now time.Time) error {
	compute, ok := e.store.(state.FinancialComputeStore)
	if !ok {
		return nil
	}
	policies, err := compute.FinancialComputePolicies(ctx, app.AccountID, app.ID)
	if err != nil {
		return err
	}
	held := false
	for _, p := range policies {
		if !p.Spec.Enabled || !financial.StrictCompute(p.Spec) {
			continue
		}
		balance, err := compute.FinancialComputeBalance(ctx, p, now)
		if err != nil {
			return err
		}
		held = held || balance.Held
	}
	rows, err := e.liveInstancesForApp(ctx, app.ID)
	if err != nil {
		return err
	}
	var failures []error
	for _, ins := range rows {
		if !state.State(ins.State).CountsForRAM() {
			continue
		}
		retire := held
		if !retire && state.State(ins.State) != state.StateWaking && state.State(ins.State) != state.StateColdBooting {
			lease, known, err := compute.FinancialComputeInstanceLease(ctx, app.AccountID, app.ID, ins.ID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			retire = known && lease.Strict && (lease.SettledAt != nil || !now.Before(lease.RunUntil))
		}
		if !retire {
			continue
		}
		if err := e.retireFinancialBudgetRuntime(ctx, ins); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
