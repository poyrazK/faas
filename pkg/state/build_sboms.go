package state

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateBuildSBOMReference(id, buildID, key string) error {
	if key == "" || (id == "" && key != "regions" && !strings.HasPrefix(key, "regions/")) {
		return nil // Preserve historical unprefixed provenance.
	}
	if !region.ValidBuildSBOMKey(id, buildID, key) {
		return ErrInvalidArgument
	}
	return nil
}

func (m *MemStore) validateBuildSBOMKeyLocked(buildID, key string) error {
	b := m.builds[buildID]
	d := m.deployments[b.DeploymentID]
	return validateBuildSBOMReference(m.apps[d.AppID].ComputeRegion, buildID, key)
}

func (s *PgStore) validateBuildSBOMKey(ctx context.Context, buildID, key string) error {
	if key == "" {
		return nil
	}
	id, err := parsePgUUID(buildID)
	if err != nil {
		return err
	}
	placement, err := sqlc.New().BuildSBOMRegion(ctx, s.pool, id)
	if err != nil {
		return mapErr(err)
	}
	return validateBuildSBOMReference(placement, buildID, key)
}
