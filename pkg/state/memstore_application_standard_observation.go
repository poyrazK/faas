package state

import (
	"context"
	"time"
)

var _ ApplicationStandardObservationStore = (*MemStore)(nil)

func (m *MemStore) ObserveApplicationStandardOperation(ctx context.Context, c ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error) {
	if !standardWorkerClaimValid(c) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	o, ok := m.applicationStandardOperations[canonicalStandardUUID(c.OperationID)]
	held := m.applicationStandardWorkerClaims[canonicalStandardUUID(c.OperationID)]
	if !ok || !sameStandardUUID(o.OrgID, c.OrgID) {
		return ApplicationStandardOperation{}, ErrNotFound
	}
	if held.Owner != c.Owner || held.Generation != c.Generation || !held.Until.After(now) || o.State == "paused" || !standardOperationActive(o.State) {
		return ApplicationStandardOperation{}, ErrApplicationStandardLeaseLost
	}
	o = cloneStandardOperation(o)
	qualified := map[string]standardApplicationQualification{}
	for _, target := range o.Targets {
		if target.State != "persisted" && target.State != "observed" {
			continue
		}
		q, err := m.qualifyStandardApplicationLocked(ctx, o.OrgID, target.AppID, target.DesiredRevision)
		if err != nil {
			return ApplicationStandardOperation{}, err
		}
		qualified[target.AppID] = q
	}
	now = time.Now().UTC().Truncate(time.Microsecond)
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	if !held.Until.After(now) {
		return ApplicationStandardOperation{}, ErrApplicationStandardLeaseLost
	}
	m.checkpointStandardObservationsLocked(&o, qualified, now)
	return m.standardMaterializationCheckpointLocked(c, o, now), nil
}

func (m *MemStore) checkpointStandardObservationsLocked(o *ApplicationStandardOperation, qualified map[string]standardApplicationQualification, now time.Time) {
	for i := range o.Targets {
		t := &o.Targets[i]
		q, ok := qualified[t.AppID]
		if !ok {
			continue
		}
		standardObservedTarget(t, q, now)
		for key, e := range m.applicationStandardEnrollments {
			if sameStandardUUID(key, t.AppID) && sameStandardUUID(e.OrgID, o.OrgID) && e.DesiredRevision == t.DesiredRevision && e.PersistedRevision == t.DesiredRevision {
				e.State, e.ObservedRevision, e.ErrorCode, e.UpdatedAt = "persisted", 0, t.ErrorCode, now
				if t.State == "observed" {
					e.State, e.ObservedRevision = "observed", e.DesiredRevision
				}
				m.applicationStandardEnrollments[key] = e
			}
		}
	}
}

func (m *MemStore) qualifyStandardApplicationLocked(ctx context.Context, orgID, appID string, revision int64) (standardApplicationQualification, error) {
	if !m.standardLogInventoryReadScopeLocked(orgID, appID) {
		return standardApplicationQualification{reason: "application_unavailable"}, nil
	}
	r := m.standardConsumerRosterLocked(appID)
	q := standardApplicationQualification{reason: standardObservationEnrollment(r, revision)}
	if q.reason != "" {
		return q, nil
	}
	q = m.qualifyStandardLoggingLocked(r)
	if q.reason != "" {
		return q, nil
	}
	for _, row := range r.LiveInstances {
		runtime, err := standardRuntimeQualification(r, row.InstanceID)
		if err != nil {
			return q, err
		}
		if runtime.Reason == "" {
			runtime, err = m.standardRuntimeQualificationLocked(ctx, runtime, m.instances[row.InstanceID])
		}
		if err != nil || !runtime.Qualified {
			q.reason = runtime.Reason
			return q, err
		}
		q.restrict(runtime.ApprovalExpiresAt)
	}
	return m.qualifyStandardArtifactsLocked(ctx, r, q)
}

func (m *MemStore) qualifyStandardLoggingLocked(r ApplicationStandardConsumerRoster) standardApplicationQualification {
	q := standardApplicationQualification{}
	inventory, _ := m.standardLogInventoryForIDLocked(r.AppID)
	inventories := []ApplicationStandardLogInventoryObservation{}
	for _, row := range m.applicationStandardLogInventories {
		inventories = append(inventories, row)
	}
	health := []ApplicationStandardLogHealthObservation{}
	for _, row := range m.applicationStandardLogHealth {
		if m.standardLogHealthProjectionLocked(AppLogDrain{ID: row.DrainID, AppID: row.AppID, StandardBinding: &row.ApplicationStandardLogDrainBinding}, row.ApplicationStandardLogHealthEvent) {
			health = append(health, row)
		}
	}
	for _, node := range r.Nodes {
		loaded := standardObservationLoggingNode(node, inventory, inventories, r.ReadAt)
		if loaded.reason != "" {
			return loaded
		}
		if loaded.until.IsZero() {
			continue
		}
		q.restrict(loaded.until)
		for _, drain := range m.appLogDrains {
			if !sameStandardUUID(drain.AppID, r.AppID) {
				continue
			}
			if binding := m.standardLogBindingLocked(drain); binding != nil {
				provider := standardObservationProvider(node, *binding, health, r.ReadAt)
				if provider.reason != "" {
					return provider
				}
				q.restrict(provider.until)
			}
		}
	}
	return m.qualifyStandardEgressLocked(r, q)
}

func (m *MemStore) qualifyStandardEgressLocked(r ApplicationStandardConsumerRoster, q standardApplicationQualification) standardApplicationQualification {
	rows := []ApplicationStandardEgressObservation{}
	for _, row := range m.applicationStandardEgressObservations {
		rows = append(rows, row)
	}
	for _, node := range r.Nodes {
		if node.NativeRequired {
			target, ok := m.standardEgressTargetLocked(r.AppID, node.NodeID)
			if !ok {
				return standardApplicationQualification{reason: "native_consumer_unavailable"}
			}
			egress := standardObservationEgress(target, rows, r.ReadAt)
			if egress.reason != "" {
				return egress
			}
			q.restrict(egress.until)
		}
		if node.NativeRequired || node.LoggingRequired && node.LoggingStoppedAt == nil {
			for _, n := range m.computeNodes {
				if sameStandardUUID(n.ID, node.NodeID) {
					q.restrict(n.LastHeartbeatAt.Add(DefaultHeartbeatStaleness))
				}
			}
		}
	}
	return q
}
