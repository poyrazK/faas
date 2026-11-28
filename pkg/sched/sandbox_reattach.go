package sched

// adr: 20261122132055 §7 — a reattach takes over a persistent session's VM
// that kept running on its node when its stream ended (schedd or agentd
// restarted): schedd books it on that node again, without placement, and
// vmmd hands the running VM to the new stream.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrSandboxNotDetached: the node holds no running VM of the session to
// reattach (its grace ended, vmmd restarted, or the node is gone).
var ErrSandboxNotDetached = errors.New("sched: no running VM of the sandbox session to reattach")

// reattachSandbox books the session's running VM on its node and serves it.
func (r *AgentRunner) reattachSandbox(ctx context.Context, a SandboxAdmission, placed func(AgentRunPlacement) error, hooks SandboxHooks,
	control <-chan SandboxControl) error {
	router, ok := r.router.(sandboxRouter)
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "the vmmd router does not support sandbox sessions")
	}
	nodeID := a.Reattach.NodeID
	n, err := r.engine.reattachNode(ctx, nodeID)
	if err != nil {
		// A lookup that failed says nothing about the VM: agentd tries the
		// reattach again within the grace.
		return fmt.Errorf("sched: reattach sandbox %s: look up node %s: %w", a.SandboxID, nodeID, err)
	}
	if inPool := n.ID != "" && (r.pool[n.ID] || r.pool[n.Name]); !inPool {
		return ErrSandboxNotDetached
	}
	// The VM is resident whether or not the ledger has room for it: book it
	// when it fits, run it either way (measured admission sees its memory).
	req := Request{Instance: a.Reattach.Instance, Plan: a.Plan, RAMMB: a.RAMMB, VCPU: a.VCPU, CPUMillicores: a.CPUMillicores,
		Kind: KindAgentRun, agentPool: r.pool, NodeID: nodeID, NodeCeilingMB: n.AdmissionCeilingMB}
	if err := r.engine.ledger.Admit(req); err == nil {
		defer r.engine.ledger.Release(req.Instance)
	} else if r.engine.log != nil {
		r.engine.log.Warn("sched: reattached sandbox not booked", "instance", req.Instance, "node_id", nodeID, "err", err)
	}
	if placed != nil {
		if err := placed(AgentRunPlacement{Instance: req.Instance, NodeID: nodeID}); err != nil {
			return err
		}
	}
	spec := SandboxSpec{Instance: req.Instance, SandboxID: a.SandboxID, AccountID: a.AccountID, Plan: a.Plan,
		VCPU: a.VCPU, MemSizeMiB: a.RAMMB, CPUMillicores: a.CPUMillicores, Network: a.network(), Persistent: true, Reattach: true}
	err = r.runSandboxWatched(ctx, router, nodeID, spec, hooks, control)
	var st interface{ GRPCStatus() *status.Status }
	if errors.As(err, &st) && st.GRPCStatus().Code() == codes.NotFound {
		return ErrSandboxNotDetached
	}
	if err != nil {
		return fmt.Errorf("sched: reattach sandbox %s on node %s: %w", a.SandboxID, nodeID, err)
	}
	return nil
}

// reattachNode finds the session's node. A node known to be gone is the
// zero node; only a failed lookup is an error.
func (e *Engine) reattachNode(ctx context.Context, nodeID string) (state.ComputeNode, error) {
	if e.nodeRegistry != nil {
		for _, n := range e.nodeRegistry.Snapshot() {
			if n.ID == nodeID {
				return n, nil
			}
		}
	}
	n, err := e.store.ComputeNodeByID(ctx, nodeID)
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return state.ComputeNode{}, nil
	}
	return n, err
}
