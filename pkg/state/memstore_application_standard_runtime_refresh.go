package state

import "context"

var _ ApplicationStandardRuntimeRefreshStore = (*MemStore)(nil)

func (m *MemStore) queueStandardRuntimeRefreshLocked(e ApplicationStandardEnrollment) {
	for i, snap := range m.snapshots {
		dep := m.deployments[snap.DeploymentID]
		if sameStandardUUID(dep.AppID, e.AppID) && !snap.Stale {
			m.snapshots[i].Stale = true
			m.deleteSnapshotReplicasLocked(snap.ID)
		}
	}
	if m.standardRuntimeRefreshRequests == nil {
		m.standardRuntimeRefreshRequests = map[string]ApplicationStandardRuntimeRefreshRequest{}
	}
	m.standardRuntimeRefreshRequests[canonicalStandardUUID(e.AppID)] = newStandardRuntimeRefresh(e)
}

func (m *MemStore) GetApplicationStandardRuntimeRefresh(ctx context.Context, orgID, appID string) (ApplicationStandardRuntimeRefreshRequest, error) {
	if !validStandardResourceRead(orgID, appID) {
		return ApplicationStandardRuntimeRefreshRequest{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardRuntimeRefreshRequest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, exists := m.standardRuntimeRefreshRequests[canonicalStandardUUID(appID)]
	if !exists || !sameStandardUUID(r.Standard.OrgID, orgID) {
		return r, ErrNotFound
	}
	return r, nil
}

func (m *MemStore) CheckApplicationStandardRuntimeRefresh(ctx context.Context, r ApplicationStandardRuntimeRefreshRequest) (bool, error) {
	if !validStandardRuntimeRefresh(r) {
		return false, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var e ApplicationStandardEnrollment
	for _, candidate := range m.applicationStandardEnrollments {
		if sameStandardUUID(candidate.AppID, r.AppID) {
			e = candidate
			break
		}
	}
	if e.AppID == "" {
		return false, ErrNotFound
	}
	paused := false
	for _, o := range m.applicationStandardOperations {
		if o.State != "paused" || !sameStandardUUID(o.OrgID, r.Standard.OrgID) {
			continue
		}
		for _, t := range o.Targets {
			paused = paused || sameStandardUUID(t.AppID, r.AppID) && t.DesiredRevision == r.Standard.DesiredRevision && t.State != "skipped"
		}
	}
	return standardRuntimeRefreshCurrent(r, e, paused)
}
