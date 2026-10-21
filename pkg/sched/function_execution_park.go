package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Freeze is the existing owner-mediated idle admission barrier. Establish it
// before dropping scheduler concurrency or taking a reuse/destroy shortcut.
// The qualified vmmd resumes it only under a capture flight. Warm capture
// recovers and releases admission, so its caller must freeze again before park.
func (e *Engine) freezeFunctionForPark(ctx context.Context, ins state.Instance) (func(), error) {
	frozen, ok := e.vmm.(FrozenVMM)
	if !ok {
		return nil, api.NewProblem(503, api.CodeInstanceBusy, "Function still running", "compute node cannot establish the execution quiescence barrier")
	}
	if _, err := frozen.FreezeInstance(ctx, ins.NodeID, ins.ID, false); err != nil {
		return nil, err
	}
	return func() {
		// Only undo a pre-capture refusal. A terminal capture or deliberate
		// retirement owns subsequent cleanup and must not be resumed here.
		recoverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		fresh, err := e.store.InstanceByID(recoverCtx, ins.ID)
		if err != nil || fresh.State != string(state.StateRunning) || fresh.WakeID != ins.WakeID || fresh.NodeID != ins.NodeID {
			return
		}
		if _, err := frozen.ThawInstance(recoverCtx, ins.NodeID, ins.ID, ThawRequest{}); err != nil {
			e.log.Warn("function park admission thaw failed", "instance", ins.ID, "error", err)
		}
	}, nil
}

func functionExecutionBusyProblem(err error) bool {
	p := api.AsProblem(err)
	return p != nil && p.Code == api.CodeInstanceBusy
}
