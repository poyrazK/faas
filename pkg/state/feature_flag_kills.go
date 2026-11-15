package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// featureFlagKillsForDeploymentSQL resolves a deployment to its project
// environment the way the runtime flags endpoint does (scope "" or "default"
// reads production) and lists the killed flag keys of that environment's
// latest published configuration (ADR 20261114150000 §3).
const featureFlagKillsForDeploymentSQL = `
SELECT coalesce(jsonb_agg(f->>'key'), '[]'::jsonb)
FROM deployments d
JOIN apps a ON a.id = d.app_id AND a.project_id IS NOT NULL
JOIN project_environments e ON e.project_id = a.project_id
 AND e.slug = CASE WHEN d.scope IN ('', 'default') THEN 'production' ELSE d.scope END
JOIN LATERAL (
  SELECT v.config FROM feature_flag_versions v
  WHERE v.environment_id = e.id
  ORDER BY v.version DESC LIMIT 1
) latest ON true
CROSS JOIN LATERAL jsonb_array_elements(latest.config->'flags') f
WHERE d.id = $1 AND f->'killed' = 'true'::jsonb`

// ListFeatureFlagEnvironments pages, by environment id, every environment
// with a published flag configuration, for the lifecycle reconciler
// (ADR 20261114150000 §4).
func (s *PgStore) ListFeatureFlagEnvironments(ctx context.Context, afterEnvironmentID string, limit int) ([]FeatureFlagAutoRolloutCandidate, error) {
	after := uuid.Nil
	if afterEnvironmentID != "" {
		parsed, err := uuid.Parse(afterEnvironmentID)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		after = parsed
	}
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT ON (e.id) v.account_id::text, v.project_id::text, e.id::text, p.slug, e.slug
FROM feature_flag_versions v
JOIN projects p ON p.id = v.project_id
JOIN project_environments e ON e.id = v.environment_id
WHERE e.id > $1
ORDER BY e.id
LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list flag environments: %w", err)
	}
	defer rows.Close()
	var out []FeatureFlagAutoRolloutCandidate
	for rows.Next() {
		var c FeatureFlagAutoRolloutCandidate
		if err := rows.Scan(&c.Scope.AccountID, &c.Scope.ProjectID, &c.Scope.EnvironmentID, &c.ProjectSlug, &c.EnvironmentSlug); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FeatureFlagEnvironmentLister is the optional Postgres capability the
// lifecycle reconciler needs.
type FeatureFlagEnvironmentLister interface {
	ListFeatureFlagEnvironments(ctx context.Context, afterEnvironmentID string, limit int) ([]FeatureFlagAutoRolloutCandidate, error)
}

// FeatureFlagKillsForDeployment returns the sorted killed flag keys of the
// deployment's project environment, or none.
func (s *PgStore) FeatureFlagKillsForDeployment(ctx context.Context, deploymentID string) ([]string, error) {
	id, parseErr := uuid.Parse(deploymentID)
	if parseErr != nil {
		// A target without a deployment UUID has no project environment,
		// so it has no kills; the gateway must not fail the request.
		return nil, nil //nolint:nilerr // an unparseable id means no kills
	}
	var raw []byte
	if err := s.pool.QueryRow(ctx, featureFlagKillsForDeploymentSQL, id).Scan(&raw); err != nil {
		return nil, fmt.Errorf("feature flag kills: %w", err)
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("feature flag kills: %w", err)
	}
	slices.Sort(keys)
	return keys, nil
}
