package state

import "time"

// putDeploymentLocked stores d and records when it stopped serving traffic,
// mirroring the deployment_serving_ended_at trigger (migration
// 20261004234807528). Default and automatic rollback order their targets
// by that stamp, so they return to the deployment that served most
// recently rather than the one created most recently. The caller holds
// m.mu.
func (m *MemStore) putDeploymentLocked(id string, d Deployment) {
	prev, existed := m.deployments[id]
	d = m.preserveFailedRollbackTargetLocked(id, d)
	m.deployments[id] = d
	switch {
	case deploymentServing(d):
		delete(m.deploymentServingEndedAt, id)
	case existed && deploymentServing(prev) && d.Status != DeploySnapshotting:
		if m.deploymentServingEndedAt == nil {
			m.deploymentServingEndedAt = map[string]time.Time{}
		}
		// Strictly increasing, so two transitions in one tick still order.
		at := time.Now().UTC()
		if !at.After(m.lastServingEndedAt) {
			at = m.lastServingEndedAt.Add(time.Nanosecond)
		}
		m.lastServingEndedAt = at
		m.deploymentServingEndedAt[id] = at
	}
}

// preserveFailedRollbackTargetLocked mirrors the rollback failure trigger
// before traffic validation, so validation sees the same alias eligibility
// that putDeploymentLocked will publish. It does not change stored intent.
func (m *MemStore) preserveFailedRollbackTargetLocked(id string, d Deployment) Deployment {
	prev, existed := m.deployments[id]
	if existed && prev.Status == DeploySnapshotting && d.Status == DeployFailed && m.deploymentServedLocked(id) {
		d.Status = DeploySuperseded
	}
	return d
}

// deploymentServedLocked reports whether id stopped serving traffic at some
// point (deployments.serving_ended_at IS NOT NULL).
func (m *MemStore) deploymentServedLocked(id string) bool {
	_, ok := m.deploymentServingEndedAt[id]
	return ok
}

// deploymentServing reports whether d currently receives traffic.
func deploymentServing(d Deployment) bool {
	return d.Status == DeployLive && d.TrafficPercent > 0
}

// rollbackRecencyLocked is the rollback ordering key, matching PgStore's
// coalesce(serving_ended_at, created_at): when d stopped serving, else when
// it was created.
func (m *MemStore) rollbackRecencyLocked(d Deployment) time.Time {
	if at, ok := m.deploymentServingEndedAt[d.ID]; ok {
		return at
	}
	return d.CreatedAt
}

// rollbackMoreRecentLocked reports whether a should be preferred over b as
// a rollback target: more recently serving, then newer, then the larger id
// (PgStore's ORDER BY ... created_at DESC, id DESC).
func (m *MemStore) rollbackMoreRecentLocked(a, b Deployment) bool {
	ra, rb := m.rollbackRecencyLocked(a), m.rollbackRecencyLocked(b)
	if !ra.Equal(rb) {
		return ra.After(rb)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID > b.ID
}
