package state

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type MCPReadinessPolicyHistoryStore interface {
	MCPReadinessStore
	ListMCPReadinessPolicyHistory(context.Context, string, string, string, string, int) (api.MCPReadinessPolicyHistory, error)
	GetMCPReadinessPolicyRevision(context.Context, string, string, string, string) (api.MCPReadinessPolicy, error)
	RestoreMCPReadinessPolicy(context.Context, string, string, string, api.RestoreMCPReadinessPolicyRequest) (api.MCPReadinessPolicy, error)
}
type readinessPolicyHistoryEntry struct {
	AppID  string
	Policy api.MCPReadinessPolicy
}

func readinessHistoryArguments(scope, before string, limit int) bool {
	return api.ValidateScope(api.MCPConnectionScope(scope)) == nil && (before == "" || uuid.Validate(before) == nil) && limit >= 1 && limit <= 50
}
func readinessRestoreRequest(old api.MCPReadinessPolicy, expected, reason string) api.SetMCPReadinessPolicyRequest {
	return api.SetMCPReadinessPolicyRequest{Scope: api.MCPConnectionScope(old.Scope), ExpectedRevision: expected, Reason: reason, Enabled: old.Enabled, Endpoint: old.Endpoint, MaxAgeSeconds: old.MaxAgeSeconds, Roles: cloneReadiness(old.Roles)}
}
func (m *MemStore) readinessHistoryPolicyLocked(app, scope, revision string) (api.MCPReadinessPolicy, error) {
	for _, entry := range m.readinessLocked().history {
		if entry.AppID == app && api.MCPConnectionScope(entry.Policy.Scope) == scope && entry.Policy.Revision == revision {
			return cloneReadiness(entry.Policy), nil
		}
	}
	return api.MCPReadinessPolicy{}, ErrNotFound
}
func (m *MemStore) GetMCPReadinessPolicyRevision(_ context.Context, account, app, scope, revision string) (api.MCPReadinessPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessPolicy{}, ErrNotFound
	}
	if !readinessHistoryArguments(scope, revision, 1) || revision == "" {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	return m.readinessHistoryPolicyLocked(app, api.MCPConnectionScope(scope), uuid.MustParse(revision).String())
}
func (m *MemStore) ListMCPReadinessPolicyHistory(_ context.Context, account, app, scope, before string, limit int) (api.MCPReadinessPolicyHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.MCPReadinessPolicyHistory{Policies: []api.MCPReadinessPolicy{}}
	if !m.readinessAppLocked(account, app) {
		return out, ErrNotFound
	}
	if !readinessHistoryArguments(scope, before, limit) {
		return out, ErrInvalidArgument
	}
	scope = api.MCPConnectionScope(scope)
	if before != "" {
		before = uuid.MustParse(before).String()
	}
	var cursor api.MCPReadinessPolicy
	if before != "" {
		var e error
		cursor, e = m.readinessHistoryPolicyLocked(app, scope, before)
		if e != nil {
			return out, e
		}
	}
	for _, entry := range m.readinessLocked().history {
		p := entry.Policy
		if entry.AppID != app || api.MCPConnectionScope(p.Scope) != scope {
			continue
		}
		if before != "" && (p.UpdatedAt.After(cursor.UpdatedAt) || p.UpdatedAt.Equal(cursor.UpdatedAt) && p.Revision >= cursor.Revision) {
			continue
		}
		out.Policies = append(out.Policies, cloneReadiness(p))
	}
	sort.Slice(out.Policies, func(i, j int) bool {
		a, b := out.Policies[i], out.Policies[j]
		if a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.Revision > b.Revision
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	if len(out.Policies) > limit {
		out.Policies = out.Policies[:limit]
		out.NextBeforeRevision = out.Policies[limit-1].Revision
	}
	return out, nil
}
func (m *MemStore) RestoreMCPReadinessPolicy(ctx context.Context, account, app, revision string, r api.RestoreMCPReadinessPolicyRequest) (api.MCPReadinessPolicy, error) {
	if api.ValidateRestoreMCPReadinessPolicy(r, revision) != nil {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	return m.setMCPReadinessPolicy(ctx, account, app, api.SetMCPReadinessPolicyRequest{Scope: r.Scope, ExpectedRevision: uuid.MustParse(r.ExpectedRevision).String(), Reason: r.Reason}, uuid.MustParse(revision).String())
}
