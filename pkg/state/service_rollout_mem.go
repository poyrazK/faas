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
	var previousID string
	var previousAt time.Time
	for _, row := range rows {
		if row.id == id || row.service {
			continue
		}
		if !row.createdAt.Before(target.CreatedAt) && !target.CreatedAt.IsZero() {
			continue
		}
		if previousID == "" || row.createdAt.After(previousAt) ||
			(row.createdAt.Equal(previousAt) && row.id > previousID) {
			previousID = row.id
			previousAt = row.createdAt
		}
	}
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
	m.deployments[id] = target
	return target, nil
}
