package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func projectEnvironmentRoutePolicyKey(appID, scope string) string {
	return appID + "\x00" + scope
}

func cloneDeclaredRoutes(routes []DeclaredRoute) []DeclaredRoute {
	out := make([]DeclaredRoute, len(routes))
	for i, route := range routes {
		out[i] = DeclaredRoute{Path: route.Path, Methods: append([]string(nil), route.Methods...)}
	}
	return out
}

func (m *MemStore) GetProjectEnvironmentRoutePolicy(_ context.Context, accountID, appID, scope string) (ProjectEnvironmentRoutePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return ProjectEnvironmentRoutePolicy{}, ErrNotFound
	}
	policy, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(appID, scope)]
	if !ok {
		return ProjectEnvironmentRoutePolicy{}, ErrNotFound
	}
	policy.DeclaredRoutes = cloneDeclaredRoutes(policy.DeclaredRoutes)
	return policy, nil
}

func (m *MemStore) PutProjectEnvironmentRoutePolicy(_ context.Context, policy ProjectEnvironmentRoutePolicy) (ProjectEnvironmentRoutePolicy, error) {
	if policy.OnlyAllowDeclaredRoutes && len(policy.DeclaredRoutes) == 0 {
		return ProjectEnvironmentRoutePolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[policy.AppID]
	if !ok || app.AccountID != policy.AccountID || app.ProjectID != policy.ProjectID || app.Status == AppDeleted {
		return ProjectEnvironmentRoutePolicy{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(policy.ProjectID, policy.EnvironmentSlug); err != nil {
		return ProjectEnvironmentRoutePolicy{}, err
	}
	key := projectEnvironmentRoutePolicyKey(policy.AppID, policy.EnvironmentSlug)
	now := time.Now().UTC()
	if prior, ok := m.projectEnvironmentRoutePolicies[key]; ok {
		policy.CreatedAt = prior.CreatedAt
	} else {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	policy.DeclaredRoutes = cloneDeclaredRoutes(policy.DeclaredRoutes)
	m.projectEnvironmentRoutePolicies[key] = policy
	policy.DeclaredRoutes = cloneDeclaredRoutes(policy.DeclaredRoutes)
	return policy, nil
}

func (s *PgStore) GetProjectEnvironmentRoutePolicy(ctx context.Context, accountID, appID, scope string) (ProjectEnvironmentRoutePolicy, error) {
	row := s.pool.QueryRow(ctx, `
		select p.account_id, p.project_id, p.app_id, p.environment_slug,
		       p.only_allow_declared_routes, p.declared_routes, p.created_at, p.updated_at
		  from project_environment_route_policies p
		  join apps a on a.id = p.app_id and a.account_id = p.account_id and a.project_id = p.project_id
		 where p.account_id = $1 and p.app_id = $2 and p.environment_slug = $3 and a.status <> 'deleted'
	`, accountID, appID, scope)
	var policy ProjectEnvironmentRoutePolicy
	var routes []byte
	if err := row.Scan(&policy.AccountID, &policy.ProjectID, &policy.AppID, &policy.EnvironmentSlug,
		&policy.OnlyAllowDeclaredRoutes, &routes, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironmentRoutePolicy{}, ErrNotFound
		}
		return ProjectEnvironmentRoutePolicy{}, mapErr(err)
	}
	if err := json.Unmarshal(routes, &policy.DeclaredRoutes); err != nil {
		return ProjectEnvironmentRoutePolicy{}, err
	}
	return policy, nil
}

func (s *PgStore) PutProjectEnvironmentRoutePolicy(ctx context.Context, policy ProjectEnvironmentRoutePolicy) (ProjectEnvironmentRoutePolicy, error) {
	if policy.OnlyAllowDeclaredRoutes && len(policy.DeclaredRoutes) == 0 {
		return ProjectEnvironmentRoutePolicy{}, ErrInvalidArgument
	}
	routeList := policy.DeclaredRoutes
	if routeList == nil {
		routeList = []DeclaredRoute{}
	}
	routes, err := json.Marshal(routeList)
	if err != nil {
		return ProjectEnvironmentRoutePolicy{}, err
	}
	row := s.pool.QueryRow(ctx, `
		insert into project_environment_route_policies
		    (account_id, project_id, app_id, environment_slug, only_allow_declared_routes, declared_routes)
		select $1, $2, $3, $4, $5, $6::jsonb
		  from apps a join project_environments e on e.project_id = a.project_id
		 where a.id = $3 and a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted'
		   and e.account_id = $1 and e.slug = $4
		on conflict (app_id, environment_slug) do update
		   set only_allow_declared_routes = excluded.only_allow_declared_routes,
		       declared_routes = excluded.declared_routes, updated_at = now()
		returning account_id, project_id, app_id, environment_slug,
		          only_allow_declared_routes, declared_routes, created_at, updated_at
	`, policy.AccountID, policy.ProjectID, policy.AppID, policy.EnvironmentSlug,
		policy.OnlyAllowDeclaredRoutes, routes)
	var stored ProjectEnvironmentRoutePolicy
	var storedRoutes []byte
	if err := row.Scan(&stored.AccountID, &stored.ProjectID, &stored.AppID, &stored.EnvironmentSlug,
		&stored.OnlyAllowDeclaredRoutes, &storedRoutes, &stored.CreatedAt, &stored.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironmentRoutePolicy{}, ErrNotFound
		}
		return ProjectEnvironmentRoutePolicy{}, mapErr(err)
	}
	if err := json.Unmarshal(storedRoutes, &stored.DeclaredRoutes); err != nil {
		return ProjectEnvironmentRoutePolicy{}, err
	}
	return stored, nil
}
