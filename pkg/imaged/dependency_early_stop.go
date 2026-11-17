package imaged

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// errDependencyFailedEarly stops a preparation pipeline after
// failIfDependencyFailed has failed its deployment. HandleNotification
// settles it as completed work: the deployment is terminal, and a retry
// would only find it so.
var errDependencyFailedEarly = errors.New("imaged: deployment stopped: a pinned dependency failed")

// settleEarlyDependencyStop completes a notification whose pipeline stopped
// on errDependencyFailedEarly: the deployment was failed on purpose, so a
// redelivery has nothing left to do.
func settleEarlyDependencyStop(err error) error {
	if errors.Is(err, errDependencyFailedEarly) {
		return nil
	}
	return err
}

// dependencyCheckedStage reports the preparation stages worth a dependency
// look before they start: each one costs build, scan or boot time a
// candidate pinned to a failed dependency can never use.
func dependencyCheckedStage(to state.StageName) bool {
	switch to {
	case state.StageSecurityScan, state.StageImageBuild, state.StageSnapshotPrepare:
		return true
	}
	return false
}

// failIfDependencyFailed fails a candidate whose pinned project dependency
// can no longer serve, before its next costly preparation stage.
//
// The activation gate used to be the only check. In a retail-store project
// deploy the UI service waited for an image slot, built, scanned and
// prepared a snapshot for six and a half minutes after the cart service it
// depends on had failed, then failed with "dependency … is unavailable"
// (retail-store GREG-01/04 follow-up). Pins never follow a later
// deployment (ADR-685), so the outcome was certain the moment the
// dependency failed.
//
// The preview neither starts the gate's deadline nor records progress, and
// only a terminal dependency_failed result stops the pipeline: a dependency
// still on its way is the activation gate's business. Store errors leave
// the pipeline running; the activation gate remains the authority.
func (h *Handler) failIfDependencyFailed(ctx context.Context, depID string) error {
	store, ok := h.store.(state.DeploymentDependencyPreviewStore)
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	if h.dependencyGateNow != nil {
		now = h.dependencyGateNow().UTC()
	}
	err := store.PreviewDeploymentDependencies(ctx, depID, now)
	var blocker *state.DependencyGateError
	if !errors.As(err, &blocker) || blocker.Code != state.CodeDependencyFailed {
		return nil
	}
	dep, err := h.store.DeploymentByID(ctx, depID)
	if err != nil || dep.Status.IsTerminal() {
		return nil
	}
	if stop, failErr := h.handleProjectDependencyBlocker(ctx, dep, blocker); !stop || failErr != nil {
		return failErr
	}
	h.log.Info("imaged: stopped deployment early; a pinned dependency failed",
		"deployment_id", depID, "app_id", dep.AppID, "detail", blocker.Detail)
	return errDependencyFailedEarly
}
