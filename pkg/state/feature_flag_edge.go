package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/flags"
)

// featureFlagEdgeForAppSQL reads the latest published flag configuration of
// an app's project environment when any of its flags routes at the edge
// (ADR-20261124212701). The gateway calls it once per app and environment and
// caches the result until feature_flags_changed.
const featureFlagEdgeForAppSQL = `
SELECT e.id::text, latest.version, latest.config
FROM apps a
JOIN project_environments e ON e.project_id = a.project_id AND e.slug = $2
JOIN LATERAL (
  SELECT v.version, v.config FROM feature_flag_versions v
  WHERE v.environment_id = e.id
  ORDER BY v.version DESC LIMIT 1
) latest ON true
WHERE a.id = $1 AND a.project_id IS NOT NULL
  AND jsonb_path_exists(latest.config, '$.flags[*].edge')`

// FeatureFlagEdgeForApp returns the edge-routed flags of the app's project
// environment (environment slug "" reads production) as a bundle; an app
// outside a project, or without edge routes, gets an empty bundle.
func (s *PgStore) FeatureFlagEdgeForApp(ctx context.Context, appID, environment string) (flags.Bundle, error) {
	id, parseErr := uuid.Parse(appID)
	if parseErr != nil {
		return flags.Bundle{}, nil //nolint:nilerr // an unparseable id has no project
	}
	if environment == "" || environment == "default" {
		environment = "production"
	}
	var (
		b   flags.Bundle
		raw []byte
	)
	err := s.pool.QueryRow(ctx, featureFlagEdgeForAppSQL, id, environment).Scan(&b.EnvironmentID, &b.Version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return flags.Bundle{}, nil
	}
	if err != nil {
		return flags.Bundle{}, fmt.Errorf("feature flag edge routes: %w", err)
	}
	var c flags.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return flags.Bundle{}, fmt.Errorf("feature flag edge routes: %w", err)
	}
	b.Config = flags.EdgeOnly(c)
	return b, nil
}
