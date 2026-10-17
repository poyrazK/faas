package state

import (
	"context"
	"strings"
	"time"
)

var _ OperatorNodeStore = (*MemStore)(nil)

func (m *MemStore) ExecuteOperatorNode(ctx context.Context, id, token string) (OperatorNodeResult, error) {
	if err := ctx.Err(); err != nil {
		return OperatorNodeResult{}, err
	}
	if err := validateSnapshotRecoveryIDs(id, token); err != nil {
		return OperatorNodeResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, exists := m.operatorIntents[id]
	if !exists {
		return OperatorNodeResult{}, ErrOperatorIntentNotFound
	}
	if !IsOperatorNodeKind(i.Kind) || i.ClaimToken != token {
		return OperatorNodeResult{}, ErrOperatorNodeClaimLost
	}
	if receipt, exists := m.operatorNodes[id]; exists {
		receipt.Replayed = true
		return receipt, nil
	}
	if i.Status != OperatorIntentRunning {
		return OperatorNodeResult{}, ErrOperatorNodeClaimLost
	}
	r := OperatorNodeResult{NodeID: i.TargetID, Status: OperatorIntentFailed, Error: "Compute node is unavailable.", FinishedAt: time.Now().UTC()}
	node, found := m.computeNodes[i.TargetID]
	if found {
		r.PreviousLifecycle, r.Lifecycle = node.Lifecycle, node.Lifecycle
		hasLive := false
		if i.Kind == OperatorIntentKindNodeRetire {
			hasLive = m.nodeMigrationPendingLocked(node.ID)
			for _, instance := range m.instances {
				if instance.NodeID == node.ID && IsLive(strings.ToLower(instance.State)) {
					hasLive = true
					break
				}
			}
		}
		next, err := operatorNodeTransition(i.Kind, node, hasLive)
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Status, r.Error, r.Lifecycle, r.Changed = OperatorIntentSucceeded, "", next, next != node.Lifecycle
		}
	}

	if r.Changed {
		next := node
		next.Lifecycle, next.Active = r.Lifecycle, r.Lifecycle.IsAdmitting()
		if r.Lifecycle == NodeLifecycleDraining || r.Lifecycle == NodeLifecycleForceDraining {
			at := r.FinishedAt
			next.DrainInitiatedAt = &at
		}
		var err error
		node, err = nextNodeLifecycle(node, next, false)
		if err != nil {
			return OperatorNodeResult{}, err
		}
	}
	outboxID, err := m.enqueueAuditEventLocked("schedd", "operator.action."+string(i.Kind)+".outcome", i.AccountID, operatorNodeOutcomeAudit(i, r), "operator.node.outcome:"+i.ID)
	if err != nil {
		return OperatorNodeResult{}, err
	}
	if r.Changed {
		m.computeNodes[node.ID] = node
	}
	i.Status, i.Error, i.FinishedAt, i.CompletionAuditOutboxID = r.Status, r.Error, &r.FinishedAt, outboxID
	i.SnapIDsMarkedStale = []string{}
	m.operatorIntents[id] = i
	r.AuditOutboxID = outboxID
	if m.operatorNodes == nil {
		m.operatorNodes = make(map[string]OperatorNodeResult)
	}
	m.operatorNodes[id] = r
	return r, nil
}
