package state

import (
	"context"
	"errors"
)

var ErrDeploymentRetryChanged = errors.New("retry target is no longer the latest failed deployment")

type deploymentRetryContextKey struct{}

// ContextWithDeploymentRetry guards creation under the same app lock used by
// every deployment creator. The guard is checked again at acceptance, closing
// the race between API preflight and another deployment being accepted.
func ContextWithDeploymentRetry(ctx context.Context, targets map[string]string) context.Context {
	return context.WithValue(ctx, deploymentRetryContextKey{}, targets)
}
func deploymentRetryTarget(ctx context.Context, appID string) string {
	targets, _ := ctx.Value(deploymentRetryContextKey{}).(map[string]string)
	if len(targets) > 0 && targets[appID] == "" {
		// A retry context must never admit a replacement or unselected app.
		return "unselected-retry-target"
	}
	return targets[appID]
}
