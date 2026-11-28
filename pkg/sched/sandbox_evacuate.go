package sched

// adr: 20261122132055 §7 — a session on an agent-pool node that starts
// draining is evacuated: schedd tells agentd once, and agentd pauses a
// persistent session (it resumes elsewhere once its capture is uploaded)
// or ends another. vmmd sends the same request when it stops.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// sandboxDrainCheckEvery is how often a session's node lifecycle is read;
// the node registry answers it without a database read.
var sandboxDrainCheckEvery = 5 * time.Second

// SandboxEvacuateNodeDraining is the reason schedd gives a draining node's
// sessions.
const SandboxEvacuateNodeDraining = "node_draining"

// sandboxNodeLeaving reports a lifecycle whose node takes no new sessions
// and is being emptied, but still runs the VMs it has.
func sandboxNodeLeaving(l state.NodeLifecycle) bool {
	switch l {
	case state.NodeLifecycleDraining, state.NodeLifecycleForceDraining, state.NodeLifecycleMaintenance, state.NodeLifecycleRetired:
		return true
	}
	return false
}

// runSandboxWatched runs the session on nodeID and evacuates it once if the
// node starts leaving the pool while it runs.
func (r *AgentRunner) runSandboxWatched(ctx context.Context, router sandboxRouter, nodeID string, spec SandboxSpec, hooks SandboxHooks,
	control <-chan SandboxControl) error {
	if hooks.Evacuate == nil {
		return router.RunSandbox(ctx, nodeID, spec, hooks, control)
	}
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	go r.watchSandboxNode(watchCtx, nodeID, hooks.Evacuate)
	return router.RunSandbox(ctx, nodeID, spec, hooks, control)
}

func (r *AgentRunner) watchSandboxNode(ctx context.Context, nodeID string, evacuate func(context.Context, string) error) {
	tick := time.NewTicker(sandboxDrainCheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		n, ok := r.engine.breakerNode(ctx, nodeID)
		if !ok || !sandboxNodeLeaving(n.Lifecycle) {
			continue
		}
		if err := evacuate(ctx, SandboxEvacuateNodeDraining); err != nil && ctx.Err() == nil && r.engine.log != nil {
			r.engine.log.Warn("sched: evacuate sandbox from a draining node", "node_id", nodeID, "err", err)
		}
		return
	}
}
