package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func validProjectEnvironmentEdgeRules(rules []ProjectEnvironmentEdgeRule) bool {
	if len(rules) > 20 {
		return false
	}
	for _, rule := range rules {
		if (rule.Kind != EdgeRuleKindHeaders && rule.Kind != EdgeRuleKindCORSA) ||
			rule.Action.Kind != rule.Kind || rule.Priority < 0 || rule.Priority > 10000 ||
			!strings.HasPrefix(rule.MatchPath, "/") || len(rule.MatchPath) > 2048 {
			return false
		}
		if rule.Kind == EdgeRuleKindHeaders && rule.Action.Headers == nil ||
			rule.Kind == EdgeRuleKindCORSA && rule.Action.CORS == nil {
			return false
		}
		if rule.Kind == EdgeRuleKindCORSA && rule.Action.CORS.CorsPresetID != nil {
			return false
		}
	}
	return true
}

func cloneProjectEnvironmentEdgeRules(rules []ProjectEnvironmentEdgeRule) []ProjectEnvironmentEdgeRule {
	if rules == nil {
		return []ProjectEnvironmentEdgeRule{}
	}
	encoded, _ := json.Marshal(rules) // typed rules contain only JSON-safe fields
	var cloned []ProjectEnvironmentEdgeRule
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func (m *MemStore) GetProjectEnvironmentEdgePolicy(_ context.Context, accountID, appID, scope string) (ProjectEnvironmentEdgePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return ProjectEnvironmentEdgePolicy{}, ErrNotFound
	}
	policy, ok := m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, scope)]
	if !ok {
		return ProjectEnvironmentEdgePolicy{}, ErrNotFound
	}
	policy.Rules = cloneProjectEnvironmentEdgeRules(policy.Rules)
	return policy, nil
}

func (m *MemStore) PutProjectEnvironmentEdgePolicy(_ context.Context, policy ProjectEnvironmentEdgePolicy) (ProjectEnvironmentEdgePolicy, error) {
	if !validProjectEnvironmentEdgeRules(policy.Rules) {
		return ProjectEnvironmentEdgePolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[policy.AppID]
	if !ok || app.AccountID != policy.AccountID || app.ProjectID != policy.ProjectID || app.Status == AppDeleted {
		return ProjectEnvironmentEdgePolicy{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(policy.ProjectID, policy.EnvironmentSlug); err != nil {
		return ProjectEnvironmentEdgePolicy{}, err
	}
	key := projectEnvironmentRoutePolicyKey(policy.AppID, policy.EnvironmentSlug)
	now := time.Now().UTC()
	if old, ok := m.projectEnvironmentEdgePolicies[key]; ok {
		policy.CreatedAt = old.CreatedAt
	} else {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	policy.Rules = cloneProjectEnvironmentEdgeRules(policy.Rules)
	m.projectEnvironmentEdgePolicies[key] = policy
	policy.Rules = cloneProjectEnvironmentEdgeRules(policy.Rules)
	return policy, nil
}

func (s *PgStore) GetProjectEnvironmentEdgePolicy(ctx context.Context, accountID, appID, scope string) (ProjectEnvironmentEdgePolicy, error) {
	row := s.pool.QueryRow(ctx, `
		select p.account_id, p.project_id, p.app_id, p.environment_slug,
		       p.rules, p.created_at, p.updated_at
		  from project_environment_edge_policies p
		  join apps a on a.id = p.app_id and a.account_id = p.account_id and a.project_id = p.project_id
		 where p.account_id = $1 and p.app_id = $2 and p.environment_slug = $3 and a.status <> 'deleted'
	`, accountID, appID, scope)
	var policy ProjectEnvironmentEdgePolicy
	var rules []byte
	if err := row.Scan(&policy.AccountID, &policy.ProjectID, &policy.AppID, &policy.EnvironmentSlug,
		&rules, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironmentEdgePolicy{}, ErrNotFound
		}
		return ProjectEnvironmentEdgePolicy{}, mapErr(err)
	}
	if err := json.Unmarshal(rules, &policy.Rules); err != nil {
		return ProjectEnvironmentEdgePolicy{}, err
	}
	return policy, nil
}

func (s *PgStore) PutProjectEnvironmentEdgePolicy(ctx context.Context, policy ProjectEnvironmentEdgePolicy) (ProjectEnvironmentEdgePolicy, error) {
	if !validProjectEnvironmentEdgeRules(policy.Rules) {
		return ProjectEnvironmentEdgePolicy{}, ErrInvalidArgument
	}
	rules := policy.Rules
	if rules == nil {
		rules = []ProjectEnvironmentEdgeRule{}
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return ProjectEnvironmentEdgePolicy{}, err
	}
	row := s.pool.QueryRow(ctx, `
		insert into project_environment_edge_policies
		    (account_id, project_id, app_id, environment_slug, rules)
		select $1, $2, $3, $4, $5::jsonb
		  from apps a join project_environments e on e.project_id = a.project_id
		 where a.id = $3 and a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted'
		   and e.account_id = $1 and e.slug = $4
		on conflict (app_id, environment_slug) do update
		   set rules = excluded.rules, updated_at = now()
		returning created_at, updated_at
	`, policy.AccountID, policy.ProjectID, policy.AppID, policy.EnvironmentSlug, encoded)
	if err := row.Scan(&policy.CreatedAt, &policy.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironmentEdgePolicy{}, ErrNotFound
		}
		return ProjectEnvironmentEdgePolicy{}, mapErr(err)
	}
	policy.Rules = cloneProjectEnvironmentEdgeRules(rules)
	return policy, nil
}
