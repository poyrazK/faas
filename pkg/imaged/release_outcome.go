// adr: 1448 — amendment 1: release commands run one at a time per app scope,
// and none after one whose outcome is unknown until a person checks it.
package imaged

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// failAfterUncertainRelease fails dep before its release command is admitted
// when an earlier release command of its app scope started but ended without
// an exit code: its worker lease expired, or it timed out or was cancelled
// while running. That migration may have changed the database partly, and
// running the next one on top of it could make it worse. The claim refuses
// such a command too (ClaimNextUnfencedAppTask), which covers one admitted
// before the earlier outcome became unknown; failing here tells the deployer
// why at once. A command already admitted keeps its own outcome.
func (h *Handler) failAfterUncertainRelease(ctx context.Context, app state.App, dep state.Deployment) (bool, error) {
	store, ok := h.store.(state.ReleaseOutcomeStore)
	if !ok {
		return false, nil
	}
	if _, err := h.store.ReleaseAppTaskByDeployment(ctx, dep.ID); err == nil {
		return false, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return true, fmt.Errorf("imaged: load deployment release task: %w", err)
	}
	prior, err := store.UncertainReleaseTask(ctx, app.AccountID, app.ID, dep.Scope, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("imaged: read uncertain release command: %w", err)
	}
	detail := uncertainReleaseDetail(prior)
	failed, err := h.store.SetDeploymentFailed(ctx, dep.ID, api.CodeReleaseOutcomeUncertain, detail)
	if err != nil {
		return true, fmt.Errorf("imaged: fail deployment after an uncertain release command: %w", err)
	}
	h.log.Warn("imaged: release command refused after an uncertain one",
		"deployment_id", dep.ID, "app_id", app.ID, "uncertain_task", prior.ID, "uncertain_deployment", prior.DeploymentID)
	h.notifyDeploymentState(ctx, failed.AppID, failed.ID, state.DeployFailed)
	return true, nil
}

func uncertainReleaseDetail(prior state.AppTask) string {
	return fmt.Sprintf("the release command of deployment %s ended with status %s without an exit code, so what it changed in the database is unknown; "+
		"this release's command did not run. Check the database, record the check with `gregale release resolve %s --note TEXT`, then deploy again",
		prior.DeploymentID, prior.Status, prior.DeploymentID)
}
