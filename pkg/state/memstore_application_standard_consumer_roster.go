package state

import (
	"context"
	"slices"
	"strings"
	"time"
)

var _ ApplicationStandardConsumerRosterStore = (*MemStore)(nil)

func (m *MemStore) GetApplicationStandardConsumerRoster(ctx context.Context, orgID, appID string) (ApplicationStandardConsumerRoster, error) {
	if !validStandardResourceRead(orgID, appID) {
		return ApplicationStandardConsumerRoster{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardConsumerRoster{}, err
	}
	if !m.standardLogInventoryReadScopeLocked(orgID, appID) {
		return ApplicationStandardConsumerRoster{}, ErrNotFound
	}
	app, e := m.standardConsumerRosterParentsLocked(appID)
	_, current := m.standardLogInventoryLocked(app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := ApplicationStandardConsumerRoster{OrgID: canonicalStandardUUID(app.OrgID), AppID: canonicalStandardUUID(app.ID), AccountID: canonicalStandardUUID(app.AccountID), DesiredRevision: e.DesiredRevision, PersistedRevision: e.PersistedRevision, EffectiveHash: e.EffectiveHash, EnrollmentState: e.State, EnrollmentCurrent: current, Nodes: []ApplicationStandardConsumerNode{}, LiveInstances: []ApplicationStandardConsumerInstance{}, ReadAt: now}
	placements := m.standardConsumerRosterInstancesLocked(&r)
	for _, n := range m.computeNodes {
		row := m.standardConsumerRosterNodeLocked(n, placements[canonicalStandardUUID(n.ID)], now)
		if row.LoggingRequired || row.NativeRequired {
			r.Nodes = append(r.Nodes, row)
		}
		delete(placements, row.NodeID)
	}
	for id := range placements {
		r.Nodes = append(r.Nodes, ApplicationStandardConsumerNode{NodeID: id, NativeRequired: true})
	}
	slices.SortFunc(r.Nodes, func(a, b ApplicationStandardConsumerNode) int { return strings.Compare(a.NodeID, b.NodeID) })
	return r, nil
}

func (m *MemStore) standardConsumerRosterParentsLocked(appID string) (App, ApplicationStandardEnrollment) {
	var app App
	var e ApplicationStandardEnrollment
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, appID) {
			app = a
			break
		}
	}
	for _, row := range m.applicationStandardEnrollments {
		if sameStandardUUID(row.AppID, appID) {
			e = row
			break
		}
	}
	return app, e
}

func (m *MemStore) standardConsumerRosterInstancesLocked(r *ApplicationStandardConsumerRoster) map[string]bool {
	nodes := map[string]bool{}
	for _, i := range m.instances {
		if sameStandardUUID(i.AppID, r.AppID) && IsLive(i.State) {
			row := ApplicationStandardConsumerInstance{InstanceID: canonicalStandardUUID(i.ID), NodeID: canonicalStandardUUID(i.NodeID), DeploymentID: canonicalStandardUUID(i.DeploymentID), State: i.State}
			r.LiveInstances = append(r.LiveInstances, row)
			nodes[row.NodeID] = true
		}
	}
	slices.SortFunc(r.LiveInstances, func(a, b ApplicationStandardConsumerInstance) int { return strings.Compare(a.InstanceID, b.InstanceID) })
	return nodes
}

func (m *MemStore) standardConsumerRosterNodeLocked(n ComputeNode, live bool, now time.Time) ApplicationStandardConsumerNode {
	id := canonicalStandardUUID(n.ID)
	role := ""
	if n.Role != nil {
		role = *n.Role
	}
	row := ApplicationStandardConsumerNode{NodeID: id, Present: true, Active: n.Active, Lifecycle: n.Lifecycle, Role: role, GatewayConfigured: n.GatewayTargetURL != nil, HeartbeatFresh: !n.LastHeartbeatAt.After(now) && now.Sub(n.LastHeartbeatAt) <= DefaultHeartbeatStaleness, LoggingRequired: role != "control-plane" && (n.Active || n.GatewayTargetURL != nil || live), NativeRequired: live, NativeIncarnation: m.computeNodeRuntimeIncarnations[n.ID], NativeProtocol: m.computeNodeRuntimeProtocols[n.ID]}
	if session, ok := m.applicationStandardLogConsumers[id]; ok {
		row.LoggingSession = &session
	}
	if closure, ok := m.applicationStandardLogConsumerClosures[id]; ok {
		row.LoggingStoppedAt = &closure.StoppedAt
	}
	return row
}
