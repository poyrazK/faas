package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var _ OperatorParkStore = (*MemStore)(nil)

func (m *MemStore) GetOperatorPark(_ context.Context, id string) (OperatorParkExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.operatorParks[id]
	if !ok {
		return e, ErrNotFound
	}
	return cloneOperatorPark(e), nil
}

func (m *MemStore) parkClaimLocked(id, token string) (OperatorIntent, error) {
	i, ok := m.operatorIntents[id]
	if !ok {
		return i, ErrOperatorIntentNotFound
	}
	if i.Kind != OperatorIntentKindForcePark || i.ClaimToken != token {
		return i, ErrOperatorParkClaimLost
	}
	return i, nil
}

func (m *MemStore) PrepareOperatorPark(ctx context.Context, id, token string, target OperatorParkTarget) (OperatorParkExecution, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorParkExecution{}, err
	}
	if err := validateParkTarget(target); err != nil {
		return OperatorParkExecution{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.parkClaimLocked(id, token)
	if err != nil {
		return OperatorParkExecution{}, err
	}
	if i.Status != OperatorIntentRunning {
		return OperatorParkExecution{}, ErrOperatorParkClaimLost
	}
	if e, ok := m.operatorParks[id]; ok {
		return cloneOperatorPark(e), nil
	}
	ins, ok := m.instances[target.InstanceID]
	target.MigrationLeaseToken, target.MigratedAt = ins.LeaseToken, ins.MigratedAt
	if !ok || !sameRestartID(i.TargetID, target.InstanceID) || !target.MatchesInstance(ins) || ins.State != target.InitialState {
		return OperatorParkExecution{}, ErrOperatorParkTargetChanged
	}
	if ins.LastRequestAt.After(target.LastRequestAt) || ins.RequestCount > target.RequestCount {
		return OperatorParkExecution{}, ErrOperatorParkCancelled
	}
	if target.Action == OperatorParkCapture {
		if m.exclusiveCaptures[ins.ID] || m.hasExclusiveOwnerLocked(ins.ID) {
			return OperatorParkExecution{}, ErrConflict
		}
		next := ins
		next.State = string(StateSnapshotting)
		next.ParkedAt = time.Now().UTC()
		if !CanTransition(State(ins.State), StateSnapshotting) {
			return OperatorParkExecution{}, ErrConflict
		}
		m.instances[ins.ID] = next
		if target.Measured {
			req := target.CaptureRequest()
			if err := req.validate(string(StateSnapshotting)); err != nil {
				m.instances[ins.ID] = ins
				return OperatorParkExecution{}, err
			}
			g, err := m.issueStandardSnapshotCaptureLocked(ctx, string(StateSnapshotting), req)
			if err != nil {
				m.instances[ins.ID] = ins
				return OperatorParkExecution{}, err
			}
			target.Grant = &g
		}
		// The owner check above ensures this cannot reject after grant issuance.
		_ = m.exclusiveRuntimeTransitionLocked(ins, next)
		if m.exclusiveCaptures == nil {
			m.exclusiveCaptures = map[string]bool{}
		}
		m.exclusiveCaptures[ins.ID] = true
	}
	e := OperatorParkExecution{IntentID: id, Target: target, Phase: ParkPrepared, UpdatedAt: time.Now().UTC()}
	if m.operatorParks == nil {
		m.operatorParks = map[string]OperatorParkExecution{}
	}
	m.operatorParks[id] = cloneOperatorPark(e)
	return cloneOperatorPark(e), nil
}

func (m *MemStore) StartOperatorPark(_ context.Context, id, token, nodeID string) (OperatorParkExecution, bool, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorParkExecution{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.parkClaimLocked(id, token)
	if err != nil {
		return OperatorParkExecution{}, false, err
	}
	if i.Status != OperatorIntentRunning {
		return OperatorParkExecution{}, false, ErrOperatorParkClaimLost
	}
	e, ok := m.operatorParks[id]
	if !ok {
		return e, false, ErrNotFound
	}
	if !sameRestartID(e.Target.NodeID, nodeID) {
		return e, false, ErrOperatorParkTargetChanged
	}
	if e.Phase != ParkPrepared {
		return cloneOperatorPark(e), false, nil
	}
	ins, ok := m.instances[e.Target.InstanceID]
	if !ok || !e.Target.MatchesInstance(ins) || State(ins.State) != e.Target.PreparedState() {
		return e, false, ErrOperatorParkTargetChanged
	}
	e.Phase, e.EffectToken, e.UpdatedAt = ParkStarted, uuid.NewString(), time.Now().UTC()
	m.operatorParks[id] = e
	return cloneOperatorPark(e), true, nil
}

func (m *MemStore) AcknowledgeOperatorPark(_ context.Context, id, token string, r OperatorParkReceipt) error {
	if err := validRestartClaim(id, token); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.operatorParks[id]
	if !ok || e.EffectToken != token {
		return ErrConflict
	}
	if err := r.Validate(e.Target); err != nil {
		return err
	}
	if e.Receipt != nil {
		if sameParkReceipt(*e.Receipt, r) {
			return nil
		}
		return ErrConflict
	}
	if e.Phase != ParkStarted && e.Phase != ParkOutcomeUnknown {
		return ErrConflict
	}
	now := time.Now().UTC()
	e.StoppedAt = &now
	e.Receipt = &r
	e.Phase = ParkAcknowledged
	e.UpdatedAt = now
	m.operatorParks[id] = cloneOperatorPark(e)
	return nil
}

func (m *MemStore) CompleteOperatorPark(_ context.Context, input OperatorParkCompletion) (OperatorParkResult, error) {
	c, err := normalizeParkCompletion(input)
	if err != nil {
		return OperatorParkResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.parkClaimLocked(c.IntentID, c.ClaimToken)
	if err != nil {
		return OperatorParkResult{}, err
	}
	if i.Status != OperatorIntentRunning {
		if i.Status == c.Status && i.Error == c.Error && i.CompletionAuditOutboxID > 0 {
			return OperatorParkResult{AuditOutboxID: i.CompletionAuditOutboxID, Replayed: true, TerminalState: m.operatorParks[i.ID].TerminalState}, nil
		}
		return OperatorParkResult{}, ErrConflict
	}
	e, has := m.operatorParks[i.ID]
	if (c.Status == OperatorIntentFailed || c.Status == OperatorIntentCancelled) && has {
		return OperatorParkResult{}, fmt.Errorf("prepared park requires verified completion or outcome_unknown: %w", ErrConflict)
	}
	var next *Instance
	if c.Status == OperatorIntentSucceeded {
		if !has || e.StoppedAt == nil || e.Receipt == nil {
			return OperatorParkResult{}, ErrConflict
		}
		ins, ok := m.instances[e.Target.InstanceID]
		terminal := e.Receipt.Outcome
		if c.DiscardCapture {
			terminal = string(StateStopped)
		}
		if !ok || !e.Target.MatchesInstance(ins) || (State(ins.State) != e.Target.PreparedState() && ins.State != terminal) {
			return OperatorParkResult{}, ErrOperatorParkTargetChanged
		}
		if ins.State != terminal && !CanTransition(State(ins.State), State(terminal)) {
			return OperatorParkResult{}, ErrConflict
		}
		if terminal == string(StateParked) && e.Target.Measured {
			record, found := m.applicationStandardSnapshotCaptures[e.Target.Grant.Token]
			if !found || record.Acknowledgment == nil || !record.Acknowledgment.Equal(*e.Receipt.Acknowledgment) {
				return OperatorParkResult{}, ErrConflict
			}
		}
		updated := ins
		updated.State = terminal
		now := time.Now().UTC()
		if terminal == string(StateStopped) {
			updated.TerminalAt = &now
		}
		if terminal == string(StateParked) {
			updated.ParkedAt = now
		}
		if now.After(updated.LastRequestAt) {
			updated.LastRequestAt = now
		}
		if err := m.exclusiveRuntimeTransitionLocked(ins, updated); err != nil {
			return OperatorParkResult{}, err
		}
		next = &updated
		e.Phase, e.TerminalState = ParkCompleted, terminal
	} else if has {
		e.Phase = ParkOutcomeUnknown
		if e.Receipt != nil {
			e.Phase = ParkAcknowledged
		}
	}
	now := time.Now().UTC()
	i.Status, i.Error, i.FinishedAt = c.Status, c.Error, &now
	var execution *OperatorParkExecution
	if has {
		e.UpdatedAt = now
		execution = &e
	}
	outboxID, err := m.enqueueAuditEventLocked("schedd", "operator.action.force_park.outcome", i.AccountID, parkOutcomeAudit(i, execution), parkAuditKey(i))
	if err != nil {
		return OperatorParkResult{}, err
	}
	if next != nil {
		m.instances[next.ID] = *next
		delete(m.exclusiveCaptures, next.ID)
		if m.hostPorts != nil {
			m.hostPorts.Release(next.NodeID, next.ID)
		}
	}
	i.CompletionAuditOutboxID = outboxID
	m.operatorIntents[i.ID] = i
	if has {
		m.operatorParks[i.ID] = e
	}
	return OperatorParkResult{AuditOutboxID: outboxID, TerminalState: e.TerminalState}, nil
}

func (m *MemStore) OperatorParkHoldsInstance(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.instances[id]
	if !ok {
		return false, nil
	}
	for _, e := range m.operatorParks {
		if e.Target.OperatorRestartTarget.MatchesInstance(i) && (e.Phase == ParkPrepared || e.Phase == ParkStarted || e.Phase == ParkAcknowledged || e.Phase == ParkOutcomeUnknown) {
			return true, nil
		}
	}
	return false, nil
}
