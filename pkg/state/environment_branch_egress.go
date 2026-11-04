package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Branch egress containment (ADR-20261031193635 section 7, slice 6a). vmmd
// reads a wake's policy from the deployment it boots, never from the request,
// so a legacy caller cannot boot a contained branch uncontained.

// ErrEnvironmentBranchGone is returned for a deployment whose project
// environment no longer exists: it may have been a contained branch deleted
// while the wake was in flight, so the wake is refused rather than booted
// with live egress.
var ErrEnvironmentBranchGone = fmt.Errorf("%w: the deployment's environment no longer exists", ErrNotFound)

// EnvironmentBranchEgressPolicy is the egress of the branch a deployment runs
// in. A deployment outside any branch has an empty Mode.
type EnvironmentBranchEgressPolicy struct {
	Mode          string
	EnvironmentID string
	Scope         string
}

// Contained reports whether the branch's instances get the contained network
// profile: both contained and simulated egress do.
func (p EnvironmentBranchEgressPolicy) Contained() bool {
	return p.Mode == EnvironmentBranchEgressContained || p.Mode == EnvironmentBranchEgressSimulated
}

// EnvironmentBranchDatabaseEndpoint is one host and port of a managed
// database bound in a branch: the only database a contained instance reaches.
type EnvironmentBranchDatabaseEndpoint struct {
	Host string
	Port int
}

// EnvironmentBranchEgressStore is vmmd's and schedd's containment authority.
type EnvironmentBranchEgressStore interface {
	// EnvironmentBranchEgress returns the policy of the deployment's branch.
	// ErrNotFound means the deployment does not exist;
	// ErrEnvironmentBranchGone that its environment was deleted.
	EnvironmentBranchEgress(ctx context.Context, deploymentID string) (EnvironmentBranchEgressPolicy, error)
	// EnvironmentBranchDatabaseEndpoints lists the endpoints of the managed
	// databases bound to the deployment's app in its scope. An unknown
	// deployment has none.
	EnvironmentBranchDatabaseEndpoints(ctx context.Context, deploymentID string) ([]EnvironmentBranchDatabaseEndpoint, error)
}

// environmentBranchLegacyScope reports scopes that never name a branch: the
// production lanes of legacy and named-environment apps.
func environmentBranchLegacyScope(scope string) bool {
	return scope == "" || scope == "default" || scope == "production"
}

func (m *MemStore) EnvironmentBranchEgress(_ context.Context, deploymentID string) (EnvironmentBranchEgressPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return EnvironmentBranchEgressPolicy{}, ErrNotFound
	}
	app, ok := m.apps[d.AppID]
	if !ok {
		return EnvironmentBranchEgressPolicy{}, ErrNotFound
	}
	scope := d.Scope
	if app.ProjectID == "" || environmentBranchLegacyScope(scope) {
		return EnvironmentBranchEgressPolicy{Scope: scope}, nil
	}
	for _, env := range m.projectEnvironments {
		if env.AccountID != app.AccountID || env.ProjectID != app.ProjectID || env.Slug != scope {
			continue
		}
		branch, ok := m.projectEnvironmentBranches[env.ID]
		if !ok {
			return EnvironmentBranchEgressPolicy{Scope: scope}, nil
		}
		return EnvironmentBranchEgressPolicy{Mode: branch.EgressMode, EnvironmentID: env.ID, Scope: scope}, nil
	}
	return EnvironmentBranchEgressPolicy{}, ErrEnvironmentBranchGone
}

// EnvironmentBranchDatabaseEndpoints is empty in MemStore, which records no
// managed database endpoints.
func (m *MemStore) EnvironmentBranchDatabaseEndpoints(context.Context, string) ([]EnvironmentBranchDatabaseEndpoint, error) {
	return nil, nil
}

// PgStore

func (s *PgStore) EnvironmentBranchEgress(ctx context.Context, deploymentID string) (EnvironmentBranchEgressPolicy, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return EnvironmentBranchEgressPolicy{}, ErrNotFound
	}
	var scope, projectID, environmentID, mode string
	err := s.pool.QueryRow(ctx, `
		select d.scope, coalesce(a.project_id::text, ''), coalesce(e.id::text, ''), coalesce(b.egress_mode, '')
		  from deployments d
		  join apps a on a.id = d.app_id
		  left join project_environments e
		         on e.account_id = a.account_id and e.project_id = a.project_id and e.slug = d.scope
		  left join project_environment_branches b on b.environment_id = e.id
		 where d.id = $1
	`, deploymentID).Scan(&scope, &projectID, &environmentID, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentBranchEgressPolicy{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentBranchEgressPolicy{}, fmt.Errorf("read environment branch egress: %w", mapErr(err))
	}
	if projectID == "" || environmentBranchLegacyScope(scope) {
		return EnvironmentBranchEgressPolicy{Scope: scope}, nil
	}
	if environmentID == "" {
		return EnvironmentBranchEgressPolicy{}, ErrEnvironmentBranchGone
	}
	if mode == "" {
		return EnvironmentBranchEgressPolicy{Scope: scope}, nil
	}
	return EnvironmentBranchEgressPolicy{Mode: mode, EnvironmentID: environmentID, Scope: scope}, nil
}

func (s *PgStore) EnvironmentBranchDatabaseEndpoints(ctx context.Context, deploymentID string) ([]EnvironmentBranchDatabaseEndpoint, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select distinct e.host, e.port
		  from deployments d
		  join managed_postgres_bindings b on b.app_id = d.app_id and b.scope = d.scope
		  join managed_postgres_database_endpoints e on e.database_id = b.database_id
		 where d.id = $1 and b.state in ('ready', 'retiring') and b.deleted_at is null
		 order by e.host, e.port
		 limit 1000
	`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list environment branch database endpoints: %w", mapErr(err))
	}
	defer rows.Close()
	var out []EnvironmentBranchDatabaseEndpoint
	for rows.Next() {
		var e EnvironmentBranchDatabaseEndpoint
		var port int32
		if err := rows.Scan(&e.Host, &port); err != nil {
			return nil, fmt.Errorf("scan environment branch database endpoint: %w", err)
		}
		e.Host, e.Port = strings.ToLower(e.Host), int(port)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list environment branch database endpoints: %w", mapErr(err))
	}
	return out, nil
}
