package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type memoryCacheReceipt struct {
	Hash     string
	Response api.EnvironmentCachePolicy
}

func cloneEnvironmentCachePolicy(in api.EnvironmentCachePolicy) api.EnvironmentCachePolicy {
	out := in
	out.Rules, _ = api.NormalizeEnvironmentCacheRules(in.Rules)
	if in.UpdatedAt != nil {
		at := *in.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}
func (m *MemStore) environmentCachePolicyLocked(account, app, scope string) (api.EnvironmentCachePolicy, error) {
	out := api.EnvironmentCachePolicy{AppID: app, Scope: scope, Inherited: true, Rules: []api.EnvironmentCacheRule{}}
	a, ok := m.apps[app]
	if !ok || a.AccountID != account || a.Status == AppDeleted {
		return out, ErrNotFound
	}
	if !api.ValidProjectEnvironmentSlug(scope) {
		return out, ErrInvalidArgument
	}
	env, err := m.projectEnvironmentBySlugLocked(a.ProjectID, scope)
	if err != nil || env.AccountID != account {
		return out, ErrNotFound
	}
	out.ProjectID, out.EnvironmentID = env.ProjectID, env.ID
	if saved, ok := m.environmentCachePolicies[memoryBudgetKey(app, scope, env.ID)]; ok {
		return cloneEnvironmentCachePolicy(saved), nil
	}
	return out, nil
}
func (m *MemStore) GetEnvironmentCachePolicy(_ context.Context, account, app, scope string) (api.EnvironmentCachePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.environmentCachePolicyLocked(account, app, scope)
}
func (m *MemStore) SetEnvironmentCachePolicy(_ context.Context, account, app, scope string, req api.SetEnvironmentCachePolicyRequest) (api.EnvironmentCachePolicy, error) {
	if api.ValidateEnvironmentCachePolicyRequest(req) != nil || req.AppID != app {
		return api.EnvironmentCachePolicy{}, ErrInvalidArgument
	}
	req.Rules, _ = api.NormalizeEnvironmentCacheRules(req.Rules)
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return current, err
	}
	if !m.accounts[account].MayDeploy() {
		return current, ErrEnvironmentCacheAccount
	}
	if current.ProjectID != req.ProjectID || current.EnvironmentID != req.EnvironmentID {
		return current, ErrRouteHealthRevision
	}
	var source api.EnvironmentCachePolicy
	if req.CopyFrom != nil {
		source, err = m.environmentCachePolicyLocked(account, app, req.CopyFrom.Scope)
		if err != nil {
			return current, err
		}
		if !environmentCacheCopyIdentity(source, current, req.CopyFrom) {
			return current, ErrRouteHealthRevision
		}
	}
	hash := environmentCacheHash(scope, req)
	key := account + "\x00" + req.RequestID
	if receipt, ok := m.environmentCacheReceipts[key]; ok {
		if receipt.Hash != hash {
			return current, ErrConflict
		}
		return cloneEnvironmentCachePolicy(receipt.Response), nil
	}
	if current.Revision != *req.ExpectedRevision || req.CopyFrom != nil && !environmentCacheCopyMatches(source, req) {
		return current, ErrRouteHealthRevision
	}
	if req.RestoreRevision != nil {
		historical, e := m.environmentCacheRevisionLocked(current, *req.RestoreRevision)
		if e != nil {
			return current, e
		}
		if !environmentCacheRestoreMatches(historical, req) {
			return current, ErrRouteHealthRevision
		}
	}
	at := time.Now().UTC()
	updated := api.EnvironmentCachePolicy{AppID: app, Scope: scope, ProjectID: req.ProjectID, EnvironmentID: req.EnvironmentID, Revision: current.Revision + 1, Rules: req.Rules, Inherited: req.Inherited, UpdatedAt: &at}
	if m.environmentCachePolicies == nil {
		m.environmentCachePolicies = map[string]api.EnvironmentCachePolicy{}
		m.environmentCacheReceipts = map[string]memoryCacheReceipt{}
	}
	historyKey := memoryBudgetKey(app, scope, req.EnvironmentID)
	if m.environmentCacheHistory == nil {
		m.environmentCacheHistory = map[string]map[int64]api.EnvironmentCachePolicy{}
	}
	if m.environmentCacheHistory[historyKey] == nil {
		m.environmentCacheHistory[historyKey] = map[int64]api.EnvironmentCachePolicy{}
	}
	m.environmentCacheHistory[historyKey][updated.Revision] = cloneEnvironmentCachePolicy(updated)
	m.environmentCachePolicies[historyKey] = cloneEnvironmentCachePolicy(updated)
	m.environmentCacheReceipts[key] = memoryCacheReceipt{Hash: hash, Response: cloneEnvironmentCachePolicy(updated)}
	return cloneEnvironmentCachePolicy(updated), nil
}
func (m *MemStore) deleteEnvironmentCachePoliciesLocked(app, environment string) {
	for key := range m.environmentCacheScheduleEvents {
		parts := strings.Split(key, "/")
		if len(parts) == 3 && (app != "" && parts[1] == app || environment != "" && parts[2] == environment) {
			delete(m.environmentCacheScheduleEvents, key)
			delete(m.environmentCacheScheduleEventFloors, key)
		}
	}
	for key, p := range m.environmentCacheSchedules {
		if app != "" && p.Value.AppID == app || environment != "" && p.Value.EnvironmentID == environment {
			delete(m.environmentCacheSchedules, key)
		}
	}
	for key, p := range m.environmentCachePurges {
		if app != "" && p.AppID == app || environment != "" && p.EnvironmentID == environment {
			delete(m.environmentCachePurges, key)
		}
	}
	for key := range m.environmentCacheGatewayObservations {
		parts := strings.Split(key, "/")
		if len(parts) == 3 && (app != "" && parts[1] == app || environment != "" && parts[2] == environment) {
			delete(m.environmentCacheGatewayObservations, key)
		}
	}

	for key, p := range m.environmentCachePolicies {
		if app != "" && p.AppID == app || environment != "" && strings.HasSuffix(key, "\x00"+environment) {
			delete(m.environmentCachePolicies, key)
			delete(m.environmentCacheHistory, key)
		}
	}
	for key, r := range m.environmentCacheReceipts {
		if app != "" && r.Response.AppID == app || environment != "" && r.Response.EnvironmentID == environment {
			delete(m.environmentCacheReceipts, key)
		}
	}
}
