package imaged

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// Deferred startup is a capability of an enrolled expiring preview. It grants
// no readiness claim: the sealed rehearsal must supply the actual boot receipt.
func (h *Handler) validateColdStartActivation(ctx context.Context, app state.App, dep state.Deployment, ready deploymentReadyPayload) error {
	m, err := h.store.ScenarioTestMemberByApp(ctx, app.ID)
	if err != nil {
		return err
	}
	if m.ColdStartJSON == "" || m.AccountID != app.AccountID || m.NetworkMode != "contained" || m.SimulationJSON == "" || ready.InstanceID != "" || app.PreviewOfSlug == "" || app.PreviewPrNumber != 0 || app.PreviewPrState != state.PreviewPrStateOpen || app.PreviewExpiresAt == nil || !app.PreviewExpiresAt.After(time.Now()) || ready.ExecutionMode == api.ExecutionModeJob {
		return errors.New("imaged: cold-start activation requires its enrolled expiring preview")
	}
	if dep.Status == state.DeployLive {
		return nil
	}
	deployments, err := h.store.ListDeploymentsForApp(ctx, app.ID, 0, 0)
	if err != nil {
		return err
	}
	if len(deployments) != 1 || deployments[0].ID != dep.ID {
		return state.ErrConflict
	}
	instances, err := h.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		return err
	}
	if len(instances) != 0 {
		return errors.New("imaged: deferred cold-start activation already has serving execution")
	}
	return nil
}
