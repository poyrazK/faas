package promotion

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// CheckRollbackContracts compares the actual graph members, whose weighted
// fallback routes can differ from the deployments selected by the graph.
func CheckRollbackContracts(ctx context.Context, store state.Store, scope string, current, restored []state.ProjectReleaseMember) error {
	if !api.ApiContractDiffEnabled() || !openapidiff.IsProductionScope(scope) {
		return nil
	}
	byApp := make(map[string]string, len(current))
	for _, member := range current {
		byApp[member.AppID] = member.DeploymentID
	}
	for _, member := range restored {
		baseline := byApp[member.AppID]
		if baseline == "" || baseline == member.DeploymentID {
			continue
		}
		check, err := openapidiff.CheckDeploymentRollback(ctx, store, member.AppID, member.DeploymentID, baseline, scope)
		if err != nil && !errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
			return fmt.Errorf("inspect graph rollback API contract: %w", err)
		}
		if check.Diff.Blocking() {
			return &openapidiff.GateError{Diff: check.Diff}
		}
	}
	return nil
}
