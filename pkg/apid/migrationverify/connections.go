package migrationverify

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ConnectionTargetReader interface {
	state.ProjectServiceReader
	AppByID(context.Context, string) (state.App, error)
	LiveDeployment(context.Context, string) (state.Deployment, error)
}

// ValidateConnectionTargets is rechecked during verification and when cutover consumes passed evidence.
// A logical name must still resolve to the same creator/project production
// target, whose selected deployment has all traffic in the caller's scope.
func ValidateConnectionTargets(ctx context.Context, reader ConnectionTargetReader, accountID, callerID string, connections []api.MigrationServiceConnectionCheck) error {
	caller, err := reader.AppByID(ctx, callerID)
	if err != nil {
		return err
	}
	if caller.AccountID != accountID || caller.ProjectID == "" || caller.PreviewOfSlug != "" || caller.Status == state.AppDeleted {
		return state.ErrConflict
	}
	dep, err := reader.LiveDeployment(ctx, callerID)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if !slices.ContainsFunc(caller.Manifest.ServiceBindings, func(b api.AppServiceBinding) bool { return b.Service == connection.Service }) {
			return state.ErrConflict
		}
		target, err := state.AppByServiceName(ctx, reader, caller, connection.Service)
		if err != nil {
			return err
		}
		if target.AccountID != accountID || target.ProjectID != caller.ProjectID || target.WorkloadName != connection.Service || target.PreviewOfSlug != "" || target.Status == state.AppDeleted || target.ID == caller.ID {
			return state.ErrConflict
		}
		live, err := reader.LiveDeployment(ctx, target.ID)
		if err != nil {
			return err
		}
		id, parseErr := uuid.Parse(live.ID)
		if parseErr != nil || id.String() != connection.DeploymentID || live.Status != state.DeployLive || live.TrafficPercent != 100 || live.Scope != dep.Scope || live.EnvironmentWorkloadHeld() {
			return state.ErrConflict
		}
	}
	return nil
}
