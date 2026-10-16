package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memoryCacheGatewaySession struct {
	ID, Target  string
	At, Started time.Time
}
type memoryCacheGatewayObservation struct {
	Session  string
	Revision int64
	At       time.Time
}

func cacheGatewayEligible(n ComputeNode) bool {
	return n.Active && n.Role != nil && (strings.TrimSpace(*n.Role) == "compute-only" || strings.TrimSpace(*n.Role) == "compute-node") && n.GatewayTargetURL != nil && strings.TrimSpace(*n.GatewayTargetURL) != ""
}
func cacheObservationFresh(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= EnvironmentCacheObservationFreshness
}
func (m *MemStore) GetEnvironmentCacheConvergence(_ context.Context, account, app, scope string) (api.EnvironmentCacheConvergence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return api.EnvironmentCacheConvergence{}, err
	}
	now := time.Now().UTC()
	out := api.EnvironmentCacheConvergence{AppID: app, ProjectID: policy.ProjectID, EnvironmentID: policy.EnvironmentID, Scope: scope, Revision: policy.Revision, CheckedAt: now}
	for _, n := range m.computeNodes {
		if !cacheGatewayEligible(n) {
			continue
		}
		out.ServingGateways++
		session, ok := m.environmentCacheGatewaySessions[n.ID]
		observed := m.environmentCacheGatewayObservations[n.ID+"/"+app+"/"+policy.EnvironmentID]
		if !ok || session.Target != *n.GatewayTargetURL || !cacheObservationFresh(n.LastHeartbeatAt, now) || !cacheObservationFresh(session.At, now) || observed.Session == session.ID && (observed.At.IsZero() || observed.At.After(now)) {
			out.StaleGateways++
		} else if observed.Session == session.ID && observed.Revision == policy.Revision {
			out.AcknowledgedGateways++
		} else {
			out.PendingGateways++
		}
	}
	cacheConvergenceStatus(&out)
	return out, nil
}
func (m *MemStore) BeginEnvironmentCacheGatewaySession(_ context.Context, node, session string, started time.Time) error {
	if _, err := uuid.Parse(session); err != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.computeNodes {
		if n.Name == node && cacheGatewayEligible(n) {
			if m.environmentCacheGatewaySessions == nil {
				m.environmentCacheGatewaySessions = map[string]memoryCacheGatewaySession{}
			}
			if old, ok := m.environmentCacheGatewaySessions[n.ID]; ok && (old.ID != session && !started.After(old.Started) || old.ID == session && (!old.Started.Equal(started) || old.Target != *n.GatewayTargetURL)) {
				return ErrConflict
			}
			m.environmentCacheGatewaySessions[n.ID] = memoryCacheGatewaySession{ID: session, Target: *n.GatewayTargetURL, At: time.Now().UTC(), Started: started}
			return nil
		}
	}
	return ErrConflict
}
func (m *MemStore) RefreshEnvironmentCacheGatewaySession(_ context.Context, node, session string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.computeNodes {
		old, ok := m.environmentCacheGatewaySessions[n.ID]
		if n.Name == node && cacheGatewayEligible(n) && ok && old.ID == session && old.Target == *n.GatewayTargetURL {
			old.At = time.Now().UTC()
			m.environmentCacheGatewaySessions[n.ID] = old
			return nil
		}
	}
	return ErrConflict
}
func (m *MemStore) ListEnvironmentCacheGatewayPolicies(_ context.Context, node, session, after string) ([]GatewayEnvironmentCachePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var nodeID string
	for _, n := range m.computeNodes {
		if n.Name == node && m.environmentCacheGatewaySessions[n.ID].ID == session {
			nodeID = n.ID
		}
	}
	if nodeID == "" {
		return nil, ErrConflict
	}
	out := []GatewayEnvironmentCachePolicy{}
	for _, p := range m.environmentCachePolicies {
		a, ok := m.apps[p.AppID]
		if !ok || a.Status == AppDeleted {
			continue
		}
		env, err := m.projectEnvironmentBySlugLocked(a.ProjectID, p.Scope)
		if err != nil || env.ID != p.EnvironmentID {
			continue
		}
		row := GatewayEnvironmentCachePolicy{AccountID: a.AccountID, AppID: p.AppID, EnvironmentID: p.EnvironmentID, Scope: p.Scope, Revision: p.Revision}
		observed := m.environmentCacheGatewayObservations[nodeID+"/"+p.AppID+"/"+p.EnvironmentID]
		if row.Key() > after && !(observed.Session == session && observed.Revision == p.Revision) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	if len(out) > EnvironmentCacheObservationBatchSize {
		out = out[:EnvironmentCacheObservationBatchSize]
	}
	return out, nil
}
func (m *MemStore) RecordEnvironmentCacheGatewayObserved(_ context.Context, node, session, app, environment string, revision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var nodeID string
	for _, n := range m.computeNodes {
		s, ok := m.environmentCacheGatewaySessions[n.ID]
		if n.Name == node && cacheGatewayEligible(n) && ok && s.ID == session && s.Target == *n.GatewayTargetURL && cacheObservationFresh(s.At, time.Now()) {
			nodeID = n.ID
		}
	}
	if nodeID == "" {
		return ErrConflict
	}
	valid := false
	for _, p := range m.environmentCachePolicies {
		a, ok := m.apps[p.AppID]
		if ok && a.Status != AppDeleted && p.AppID == app && p.EnvironmentID == environment && p.Revision == revision {
			valid = true
		}
	}
	if !valid || revision < 1 {
		return ErrConflict
	}
	if m.environmentCacheGatewayObservations == nil {
		m.environmentCacheGatewayObservations = map[string]memoryCacheGatewayObservation{}
	}
	m.environmentCacheGatewayObservations[nodeID+"/"+app+"/"+environment] = memoryCacheGatewayObservation{Session: session, Revision: revision, At: time.Now().UTC()}
	return nil
}
