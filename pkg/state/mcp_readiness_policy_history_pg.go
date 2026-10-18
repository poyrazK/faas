package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func readReadinessHistoryPolicy(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, app, scope, revision string) (api.MCPReadinessPolicy, error) {
	var raw []byte
	if e := q.QueryRow(ctx, `SELECT policy FROM app_mcp_readiness_policy_history WHERE app_id=$1 AND scope=$2 AND revision=$3`, app, scope, revision).Scan(&raw); e != nil {
		return api.MCPReadinessPolicy{}, mapErr(e)
	}
	var out api.MCPReadinessPolicy
	e := json.Unmarshal(raw, &out)
	out.Scope = scope
	return out, e
}
func (s *PgStore) GetMCPReadinessPolicyRevision(ctx context.Context, account, app, scope, revision string) (api.MCPReadinessPolicy, error) {
	if _, e := s.GetMCPReadinessPolicy(ctx, account, app, scope); e != nil {
		return api.MCPReadinessPolicy{}, e
	}
	if !readinessHistoryArguments(scope, revision, 1) || revision == "" {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	return readReadinessHistoryPolicy(ctx, s.pool, app, api.MCPConnectionScope(scope), revision)
}
func (s *PgStore) ListMCPReadinessPolicyHistory(ctx context.Context, account, app, scope, before string, limit int) (api.MCPReadinessPolicyHistory, error) {
	out := api.MCPReadinessPolicyHistory{Policies: []api.MCPReadinessPolicy{}}
	if _, e := s.GetMCPReadinessPolicy(ctx, account, app, scope); e != nil {
		return out, e
	}
	if !readinessHistoryArguments(scope, before, limit) {
		return out, ErrInvalidArgument
	}
	scope = api.MCPConnectionScope(scope)
	if before != "" {
		if _, e := readReadinessHistoryPolicy(ctx, s.pool, app, scope, before); e != nil {
			return out, e
		}
	}

	query := `SELECT h.policy FROM app_mcp_readiness_policy_history h WHERE h.app_id=$1 AND h.scope=$2`
	args := []any{app, scope, limit + 1}
	if before != "" {
		query += ` AND (h.recorded_at,h.revision)<(SELECT c.recorded_at,c.revision FROM app_mcp_readiness_policy_history c WHERE c.app_id=$1 AND c.scope=$2 AND c.revision=$3) ORDER BY h.recorded_at DESC,h.revision DESC LIMIT $4`
		args = []any{app, scope, before, limit + 1}
	} else {
		query += ` ORDER BY h.recorded_at DESC,h.revision DESC LIMIT $3`
	}
	rows, e := s.pool.Query(ctx, query, args...)

	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var p api.MCPReadinessPolicy
		if e = rows.Scan(&raw); e != nil {
			return out, e
		}
		if e = json.Unmarshal(raw, &p); e != nil {
			return out, e
		}
		p.Scope = scope
		out.Policies = append(out.Policies, p)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Policies) > limit {
		out.Policies = out.Policies[:limit]
		out.NextBeforeRevision = out.Policies[limit-1].Revision
	}
	return out, nil
}
func (s *PgStore) RestoreMCPReadinessPolicy(ctx context.Context, account, app, revision string, r api.RestoreMCPReadinessPolicyRequest) (api.MCPReadinessPolicy, error) {
	if api.ValidateRestoreMCPReadinessPolicy(r, revision) != nil {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	return s.setMCPReadinessPolicy(ctx, account, app, api.SetMCPReadinessPolicyRequest{Scope: r.Scope, ExpectedRevision: uuid.MustParse(r.ExpectedRevision).String(), Reason: r.Reason}, uuid.MustParse(revision).String())
}
