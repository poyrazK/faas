package state

import (
	"context"
	"strings"
	"time"
)

var _ NodeLifecycleCompletionStore = (*MemStore)(nil)

type nodeLifecycleCompletionKey struct {
	NodeID     string
	Generation int64
}

func (m *MemStore) storeComputeNodeLifecycleLocked(node ComputeNode) (ComputeNode, error) {
	if (node.Lifecycle == NodeLifecycleMaintenance || node.Lifecycle == NodeLifecycleRetired) && m.nodeMigrationPendingLocked(node.ID) {
		return ComputeNode{}, ErrConflict
	}
	// Mirror the readiness column: a write that does not set it keeps the
	// stored value, and a new row defaults to ready.
	if node.Readiness == "" {
		if prior, ok := m.computeNodes[node.ID]; ok && prior.Readiness != "" {
			node.Readiness, node.ReadinessReason = prior.Readiness, prior.ReadinessReason
		} else {
			node.Readiness = NodeReadinessReady
		}
	}
	next, err := nextNodeLifecycle(m.computeNodes[node.ID], node, false)
	if err != nil {
		return ComputeNode{}, err
	}
	m.computeNodes[node.ID] = next
	return next, nil
}

func (m *MemStore) CompleteNodeLifecycle(ctx context.Context, in NodeLifecycleCompletion) (NodeLifecycleCompletionResult, error) {
	if err := ctx.Err(); err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	if err := in.validate(); err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := nodeLifecycleCompletionKey{NodeID: in.NodeID, Generation: in.Generation}
	if r, ok := m.nodeLifecycleCompletions[key]; ok {
		if r.Lifecycle != in.Lifecycle {
			return NodeLifecycleCompletionResult{}, ErrNodeLifecycleChanged
		}
		r.Replayed = true
		return r, nil
	}
	node, ok := m.computeNodes[in.NodeID]
	if !ok {
		return NodeLifecycleCompletionResult{}, ErrNotFound
	}
	if node.LifecycleGeneration != in.Generation || node.Lifecycle != in.Lifecycle {
		return NodeLifecycleCompletionResult{}, ErrNodeLifecycleChanged
	}
	if m.nodeMigrationPendingLocked(in.NodeID) {
		return NodeLifecycleCompletionResult{}, ErrConflict
	}
	for _, instance := range m.instances {
		if instance.NodeID != in.NodeID {
			continue
		}
		current := strings.ToLower(instance.State)
		blocked := IsLive(current)
		if in.Lifecycle == NodeLifecycleRecovering {
			blocked = current == string(StateMigrating) || current == string(StateSnapshotting)
		}
		if blocked {
			return NodeLifecycleCompletionResult{}, ErrConflict
		}
	}
	r := nodeCompletionResult(in, node, time.Now().UTC())
	next := node
	next.Lifecycle, next.Active = r.ResultLifecycle, r.ResultLifecycle.IsAdmitting()
	if r.ResultLifecycle == NodeLifecycleMaintenance {
		at := r.CompletedAt
		next.DrainCompletedAt = &at
	} else {
		outcome := "succeeded"
		next.LastRecoveryOutcome = &outcome
	}
	next, err := nextNodeLifecycle(node, next, true)
	if err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	r.AuditOutboxID, err = m.enqueueAuditEventLocked("schedd", r.Kind(), nil, r.auditData(), r.dedupeKey())
	if err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	m.computeNodes[node.ID] = next
	if m.nodeLifecycleCompletions == nil {
		m.nodeLifecycleCompletions = make(map[nodeLifecycleCompletionKey]NodeLifecycleCompletionResult)
	}
	m.nodeLifecycleCompletions[key] = r
	return r, nil
}
