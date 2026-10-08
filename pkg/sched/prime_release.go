package sched

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// A notification or recovery sweep cannot authorize serving before the pinned
// release task succeeds. Keep the fence at Prime as well as its recovery index.
func (e *Engine) releaseAllowsPrime(ctx context.Context, dep state.Deployment) (bool, error) {
	if len(dep.ReleaseCommand) == 0 {
		return true, nil
	}
	store, ok := e.store.(state.AppTaskStore)
	if !ok {
		return false, fmt.Errorf("sched: release task store is unavailable")
	}
	task, err := store.ReleaseAppTaskByDeployment(ctx, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sched: read release task before prime: %w", err)
	}
	scope := dep.Scope
	if scope == "" {
		scope = "default"
	}
	return task.Status == state.AppTaskSucceeded && task.Kind == state.AppTaskKindRelease &&
		task.AppID == dep.AppID && task.DeploymentID == dep.ID &&
		task.ArtifactKey == dep.RootfsKey && task.ImageDigest == dep.ImageDigest &&
		task.DeploymentScope == scope && task.CommandShell == dep.ReleaseCommandShell &&
		slices.Equal(task.Command, dep.ReleaseCommand), nil
}
