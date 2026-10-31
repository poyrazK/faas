package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Edge-rule scopes (ADR-091 amendment 2026-10-31, round 6). A company app
// keeps its creator's account, so an account no longer names one owner: the
// creator's personal apps and the company apps they created share it. A
// rule's host pattern and route target stay inside its app's scope: the
// organization that owns the app, or "" for a personal app (no organization,
// or the account's personal one). Without it, a "*" rule on a personal app
// applied to the company's hosts, and a company admin's "*" rule to the
// creator's personal apps.

// EdgeRuleScopeStore resolves apps' edge-rule scopes in one read.
type EdgeRuleScopeStore interface {
	// EdgeRuleScopes returns each known app's scope; unknown ids are absent.
	EdgeRuleScopes(ctx context.Context, appIDs []string) (map[string]string, error)
}

var (
	_ EdgeRuleScopeStore = (*PgStore)(nil)
	_ EdgeRuleScopeStore = (*MemStore)(nil)
)

// AppEdgeRuleScope returns app's edge-rule scope.
func AppEdgeRuleScope(ctx context.Context, orgs interface {
	OrgByID(context.Context, string) (Org, error)
}, app App) (string, error) {
	if app.OrgID == "" {
		return "", nil
	}
	org, err := orgs.OrgByID(ctx, app.OrgID)
	if err != nil {
		return "", fmt.Errorf("state: edge rule scope of app %s: %w", app.ID, err)
	}
	if org.Personal {
		return "", nil
	}
	return org.ID, nil
}

func (s *PgStore) EdgeRuleScopes(ctx context.Context, appIDs []string) (map[string]string, error) {
	ids := make([]string, 0, len(appIDs))
	for _, id := range appIDs {
		if _, err := uuid.Parse(id); err == nil {
			ids = append(ids, id)
		}
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		select a.id::text, coalesce(case when o.personal_org then null else o.id::text end, '')
		  from apps a
		  left join orgs o on o.id = a.org_id
		 where a.id = any($1::uuid[])
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("state: edge rule scopes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, scope string
		if err := rows.Scan(&id, &scope); err != nil {
			return nil, fmt.Errorf("state: scan edge rule scope: %w", err)
		}
		out[id] = scope
	}
	return out, rows.Err()
}

func (m *MemStore) EdgeRuleScopes(_ context.Context, appIDs []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(appIDs))
	for _, id := range appIDs {
		app, ok := m.apps[id]
		if !ok {
			continue
		}
		scope := ""
		if app.OrgID != "" {
			if org, known := m.orgs[app.OrgID]; !known || !org.Personal {
				scope = app.OrgID
			}
		}
		out[id] = scope
	}
	return out, nil
}
