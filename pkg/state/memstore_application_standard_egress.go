package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"slices"
	"strings"
	"time"
)

var _ ApplicationStandardEgressStore = (*MemStore)(nil)

func (m *MemStore) standardEgressTargetLocked(appID, nodeID string) (ApplicationStandardEgressTarget, bool) {
	var app App
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, appID) {
			app = a
			break
		}
	}
	_, enrollment, ok := m.standardLogParentsLocked(AppLogDrain{AppID: app.ID, AccountID: app.AccountID})
	if !ok || enrollment.State != "persisted" && enrollment.State != "observed" || !m.standardLogNodeEligibleLocked(nodeID) || !m.standardEgressLiveLocked(appID, nodeID) {
		return ApplicationStandardEgressTarget{}, false
	}
	p := runtimeadmission.EgressPolicy{AppID: canonicalStandardUUID(app.ID), Revision: m.appEgressRevisionLocked(app.ID), Allowlist: app.EgressAllowlist, Ports: app.EgressPorts}.Clone()
	hash, err := p.Hash()
	if err != nil {
		return ApplicationStandardEgressTarget{}, false
	}
	i := runtimeadmission.Identity{NodeID: canonicalStandardUUID(nodeID), Incarnation: m.computeNodeRuntimeIncarnations[nodeID], ProtocolVersion: m.computeNodeRuntimeProtocols[nodeID]}
	return ApplicationStandardEgressTarget{OrgID: canonicalStandardUUID(app.OrgID), AppID: p.AppID, DesiredRevision: enrollment.DesiredRevision, EffectiveHash: enrollment.EffectiveHash, Identity: i, Policy: p, PolicyHash: hash}, true
}

func (m *MemStore) standardEgressLiveLocked(appID, nodeID string) bool {
	for _, ins := range m.instances {
		if sameStandardUUID(ins.AppID, appID) && sameStandardUUID(ins.NodeID, nodeID) && IsLive(ins.State) {
			return true
		}
	}
	return false
}

func (m *MemStore) ListPendingApplicationStandardEgress(ctx context.Context, appID string) ([]ApplicationStandardEgressTarget, error) {
	if appID != "" && !validStandardResourceRead(appID, appID) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targets := []ApplicationStandardEgressTarget{}
	seen := map[string]bool{}
	for _, ins := range m.instances {
		key := canonicalStandardUUID(ins.AppID) + "\x00" + canonicalStandardUUID(ins.NodeID)
		if seen[key] || appID != "" && !sameStandardUUID(ins.AppID, appID) {
			continue
		}
		seen[key] = true
		t, ok := m.standardEgressTargetLocked(ins.AppID, ins.NodeID)
		if !ok {
			continue
		}
		o, exists := m.applicationStandardEgressObservations[key]
		if !exists || !sameStandardEgress(t, o.Target) || time.Since(o.ObservedAt) >= api.ApplicationStandardEgressFreshness/2 {
			targets = append(targets, t)
		}
	}
	slices.SortFunc(targets, func(a, b ApplicationStandardEgressTarget) int {
		return strings.Compare(a.AppID+"\x00"+a.Identity.NodeID, b.AppID+"\x00"+b.Identity.NodeID)
	})
	if len(targets) > api.ApplicationStandardEgressBatchLimit {
		targets = targets[:api.ApplicationStandardEgressBatchLimit]
	}
	return targets, nil
}

func (m *MemStore) RecordApplicationStandardEgress(ctx context.Context, t ApplicationStandardEgressTarget, r runtimeadmission.EgressReceipt) (ApplicationStandardEgressObservation, error) {
	if !t.valid() || r.Check(t.Identity, t.Policy) != nil {
		return ApplicationStandardEgressObservation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEgressObservation{}, err
	}
	current, ok := m.standardEgressTargetLocked(t.AppID, t.Identity.NodeID)
	if !ok || !sameStandardEgress(t, current) {
		return ApplicationStandardEgressObservation{}, ErrApplicationStandardRuntimeStale
	}
	o := ApplicationStandardEgressObservation{Target: t.clone(), Receipt: r, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if m.applicationStandardEgressObservations == nil {
		m.applicationStandardEgressObservations = map[string]ApplicationStandardEgressObservation{}
	}
	m.applicationStandardEgressObservations[t.AppID+"\x00"+t.Identity.NodeID] = o
	o.Target = o.Target.clone()
	return o, nil
}

func (m *MemStore) ListApplicationStandardEgress(ctx context.Context, orgID, appID string) ([]ApplicationStandardEgressObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.standardLogInventoryReadScopeLocked(orgID, appID) {
		return nil, ErrNotFound
	}
	result := []ApplicationStandardEgressObservation{}
	for _, o := range m.applicationStandardEgressObservations {
		if o.Target.AppID != canonicalStandardUUID(appID) {
			continue
		}
		current, ok := m.standardEgressTargetLocked(appID, o.Target.Identity.NodeID)
		if ok && sameStandardEgress(current, o.Target) && time.Since(o.ObservedAt) < api.ApplicationStandardEgressFreshness {
			o.Target = o.Target.clone()
			result = append(result, o)
		}
	}
	slices.SortFunc(result, func(a, b ApplicationStandardEgressObservation) int {
		return strings.Compare(a.Target.Identity.NodeID, b.Target.Identity.NodeID)
	})
	return result, nil
}

func (m *MemStore) eraseStandardEgressLocked(appID, nodeID string) {
	for key, o := range m.applicationStandardEgressObservations {
		if appID != "" && sameStandardUUID(o.Target.AppID, appID) || nodeID != "" && sameStandardUUID(o.Target.Identity.NodeID, nodeID) {
			delete(m.applicationStandardEgressObservations, key)
		}
	}
}
