package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// destroyFork ends a production fork (ADR-732) without a snapshot: destroy
// the VM, release its RAM reservation, and stop the row. The caller holds
// the app lock. Used by every path that would otherwise park or snapshot a
// fork, so a fork's memory can never become a restorable capture.
func (e *Engine) destroyFork(ctx context.Context, ins state.Instance) error {
	if !state.IsFork(ins.Mode) {
		return fmt.Errorf("sched: destroy fork %s: mode %q is not a fork", ins.ID, ins.Mode)
	}
	if err := e.timedDestroy(context.WithoutCancel(ctx), ins.NodeID, ins.ID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: destroy fork %s: %w", ins.ID, err)
	}
	e.ledger.Release(ins.ID)
	e.transition(ctx, ins.ID, ins.AppID, state.StateStopped)
	return nil
}
