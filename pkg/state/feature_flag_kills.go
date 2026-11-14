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

// FeatureFlagKillsForDeployment returns the sorted killed flag keys of the
// deployment's project environment, or none.
func (s *PgStore) FeatureFlagKillsForDeployment(ctx context.Context, deploymentID string) ([]string, error) {
	id, err := uuid.Parse(deploymentID)
	if err != nil {
		return nil, nil
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
