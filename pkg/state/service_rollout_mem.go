package state

import (
	"context"
	"time"
)

func deploymentPreferredForWake(candidate, current Deployment) bool {
	candidateServing := candidate.TrafficPercent > 0
	currentServing := current.TrafficPercent > 0
	if candidateServing != currentServing {
		return candidateServing
	}
	if candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.ID > current.ID
	}
	return candidate.CreatedAt.After(current.CreatedAt)
}

type memServiceRolloutLiveRow struct {
	id        string
	createdAt time.Time
	service   bool
}

func previousMemServiceRolloutRow(target Deployment, rows []memServiceRolloutLiveRow) (memServiceRolloutLiveRow, bool) {
	var previous memServiceRolloutLiveRow
	found := false
	for _, row := range rows {
		if row.id == target.ID || row.service {
			continue
		}
		if !row.createdAt.Before(target.CreatedAt) && !target.CreatedAt.IsZero() {
			continue
		}
		if !found || row.createdAt.After(previous.createdAt) ||
			(row.createdAt.Equal(previous.createdAt) && row.id > previous.id) {
			previous = row
			found = true
		}
	}
	return previous, found
}

func (m *MemStore) serviceRolloutTargetLocked(id string) (Deployment, []memServiceRolloutLiveRow, error) {
	target, ok := m.deployments[id]
	if !ok {
		return Deployment{}, nil, ErrNotFound
	}
	if target.Status != DeployLive || !IsServiceRollout(target) {
		return Deployment{}, nil, ErrServiceRolloutInvalid
	}
	rows := make([]memServiceRolloutLiveRow, 0)
	for otherID, other := range m.deployments {
		if other.AppID != target.AppID ||
			normalizedDeploymentScope(other.Scope) != normalizedDeploymentScope(target.Scope) ||
			other.Status != DeployLive {
			continue
		}
		rows = append(rows, memServiceRolloutLiveRow{
			id:        otherID,
			createdAt: other.CreatedAt,
			service:   IsServiceRollout(other),
		})
	}
	return target, rows, nil
}

// FinalizeServiceRollout is the in-memory mirror of PgStore's atomic
// readiness-gated promotion. m.mu covers both the sibling cleanup and the
// target promotion, so a concurrent reconcile cannot observe a split state.
func (m *MemStore) FinalizeServiceRollout(_ context.Context, id string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, rows, err := m.serviceRolloutTargetLocked(id)
	if err != nil {
		return Deployment{}, err
	}
	if target.ServiceRolloutHandoff.ActiveAbort() {
		return target, ErrServiceRolloutInvalid
	}
	for _, row := range rows {
		if row.id == id {
			continue
		}
		other := m.deployments[row.id]
		other.Status = DeploySuperseded
		other.TrafficPercent = 0
		m.deployments[row.id] = other
	}
	now := time.Now().UTC()
	target.TrafficPercent = 100
	target.RolloutState = "complete"
	target.RolloutCompletedAt = &now
	target.RolloutAbortedAt = nil
	target.RolloutAbortedReason = ""
	handoff := target.ServiceRolloutHandoff
	handoff.Action = ServiceRolloutActionPromote
	handoff.Phase = ServiceRolloutPhaseComplete
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = &now
	target.ServiceRolloutHandoff = handoff
	m.deployments[id] = target
	return target, nil
}

// BeginServiceRolloutCutover mirrors the PostgreSQL two-phase handoff. It
// changes only traffic weights; every generation remains live until the
// scheduler completes the gateway acknowledgement and request-drain barriers.
func (m *MemStore) BeginServiceRolloutCutover(_ context.Context, id string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, rows, err := m.serviceRolloutTargetLocked(id)
	if err != nil {
		return Deployment{}, err
	}
	if target.ServiceRolloutHandoff.ActiveAbort() {
		return target, ErrServiceRolloutInvalid
	}
	for _, row := range rows {
		other := m.deployments[row.id]
		if row.id == id {
			other.TrafficPercent = 100
		} else {
			other.TrafficPercent = 0
		}
		m.deployments[row.id] = other
	}
	previous, _ := previousMemServiceRolloutRow(target, rows)
	now := time.Now().UTC()
	handoff := target.ServiceRolloutHandoff
	if handoff.StartedAt == nil || handoff.Action != ServiceRolloutActionPromote {
		handoff.StartedAt = &now
	}
	handoff.Action = ServiceRolloutActionPromote
	handoff.Phase = ServiceRolloutPhaseRouting
	handoff.PredecessorDeploymentID = previous.id
	handoff.RetryCount++
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = nil
	target = m.deployments[target.ID]
	target.ServiceRolloutHandoff = handoff
	m.deployments[target.ID] = target
	return m.deployments[target.ID], nil
}

func (m *MemStore) BeginServiceRolloutAbort(_ context.Context, id string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, rows, err := m.serviceRolloutTargetLocked(id)
	if err != nil {
		return Deployment{}, err
	}
	if !target.ServiceRolloutHandoff.ActiveAbort() {
		return target, ErrServiceRolloutInvalid
	}
	previous, found := previousMemServiceRolloutRow(target, rows)
	if !found || (target.ServiceRolloutHandoff.PredecessorDeploymentID != "" && target.ServiceRolloutHandoff.PredecessorDeploymentID != previous.id) {
		return target, ErrServiceRolloutInvalid
	}
	for _, row := range rows {
		other := m.deployments[row.id]
		if row.id == previous.id {
			other.TrafficPercent = 100
		} else {
			other.TrafficPercent = 0
		}
		m.deployments[row.id] = other
	}
	now := time.Now().UTC()
	handoff := target.ServiceRolloutHandoff
	handoff.Phase = ServiceRolloutPhaseRouting
	handoff.PredecessorDeploymentID = previous.id
	handoff.RetryCount++
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = nil
	target = m.deployments[target.ID]
	target.ServiceRolloutHandoff = handoff
	m.deployments[target.ID] = target
	return target, nil
}

func (m *MemStore) UpdateServiceRolloutHandoff(_ context.Context, id string, handoff ServiceRolloutHandoff) (Deployment, error) {
	if !validServiceRolloutHandoff(handoff) {
		return Deployment{}, ErrServiceRolloutInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	if target.Status != DeployLive || !IsServiceRollout(target) {
		return target, ErrServiceRolloutInvalid
	}
	if !serviceRolloutHandoffCanReplace(target.ServiceRolloutHandoff, handoff) {
		return target, ErrServiceRolloutInvalid
	}
	target.ServiceRolloutHandoff = handoff
	m.deployments[id] = target
	return target, nil
}

// AbortServiceRollout is the in-memory mirror of PgStore's atomic rollback.
// It restores the newest older stable live row and closes every other live
// sibling so a failed rollout cannot leave an ambiguous serving set.
func (m *MemStore) AbortServiceRollout(_ context.Context, id, reason string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, rows, err := m.serviceRolloutTargetLocked(id)
	if err != nil {
		return Deployment{}, err
	}
	previous, _ := previousMemServiceRolloutRow(target, rows)
	previousID := previous.id
	for _, row := range rows {
		if row.id == id {
			continue
		}
		other := m.deployments[row.id]
		if row.id == previousID {
			other.Status = DeployLive
			other.TrafficPercent = 100
		} else {
			other.Status = DeploySuperseded
			other.TrafficPercent = 0
		}
		m.deployments[row.id] = other
	}
	now := time.Now().UTC()
	target.Status = DeploySuperseded
	target.TrafficPercent = 0
	target.RolloutState = "aborted"
	target.RolloutCompletedAt = nil
	target.RolloutAbortedAt = &now
	target.RolloutAbortedReason = reason
	handoff := target.ServiceRolloutHandoff
	handoff.Action = ServiceRolloutActionAbort
	handoff.Phase = ServiceRolloutPhaseComplete
	handoff.Reason = reason
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = &now
	target.ServiceRolloutHandoff = handoff
	m.deployments[id] = target
	return target, nil
}
