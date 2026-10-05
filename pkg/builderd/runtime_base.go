package builderd

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/state"
)

// An explicit update target wins over daemon defaults. Errors are never
// interpreted as an unpinned build, including failures reading the pin.
func resolveDeploymentRuntimeBaseRef(ctx context.Context, store any, app state.App, dep state.Deployment, fw Framework, envLookup func(string) string) (string, error) {
	if targets, ok := store.(state.RuntimeUpgradeTargetStore); ok {
		target, err := targets.DeploymentRuntimeUpgradeTarget(ctx, dep.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return "", fmt.Errorf("read runtime update target: %w", err)
		}
		if err == nil {
			if target.Validate() != nil || app.ID != dep.AppID || app.Type != state.AppTypeFunction ||
				app.Runtime != target.Runtime || target.Architecture != runtime.GOARCH || fw == FrameworkDocker || dep.SourceSHA256 == "" {
				return "", fmt.Errorf("%w: incompatible runtime update build target", state.ErrConflict)
			}
			return target.SourceRef, nil
		}
	}
	return resolveBuildRuntimeBaseRef(app.Runtime, fw, envLookup)
}

// resolveBuildRuntimeBaseRef chooses the same base reference imaged uses for
// deployment-layer materialisation. The app runtime is authoritative,
// including the empty runtime, which intentionally resolves to the minimal
// base for plain app deployments. Using a framework-derived fallback here
// would make builderd and imaged select different bases for legacy apps.
func resolveBuildRuntimeBaseRef(runtime string, fw Framework, envLookup func(string) string) (string, error) {
	if fw == FrameworkDocker {
		// Dockerfile builds own their FROM chain; injecting a Railpack base
		// into the source would change customer Dockerfile semantics.
		return "", nil
	}
	return imaged.ResolveDeployBaseRef(runtime, envLookup)
}
