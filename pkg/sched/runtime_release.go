package sched

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/onebox-faas/faas/pkg/state"
)

// artifactBaseKey uses the physical layer binding across wake, prime,
// migration and tasks. Legacy artifacts retain the legacy logical base.
// deploymentBaseKey is the drive0 base for a deployment's layer. A source-built
// container app whose layer was built for a shared runner base boots that
// base (docs/adr/1500-shared-runtime-source-apps.md, H10-140); every other
// deployment keeps artifactBaseKey's runtime-release or app-runtime rule.
func (e *Engine) deploymentBaseKey(ctx context.Context, app state.App, dep state.Deployment, key string) (string, error) {
	if dep.RuntimeBase == "" {
		return e.artifactBaseKey(ctx, app, key)
	}
	if app.Type == state.AppTypeFunction || app.Runtime != "" || !state.SharedSourceAppRuntime(dep.RuntimeBase) {
		return "", errors.New("sched: deployment runtime base does not match its app")
	}
	return baseKey(dep.RuntimeBase), nil
}

func (e *Engine) artifactBaseKey(ctx context.Context, app state.App, key string) (string, error) {
	releases, ok := e.store.(state.RuntimeReleaseStore)
	if !ok {
		return baseKey(app.Runtime), nil
	}
	r, err := releases.RuntimeReleaseForArtifact(ctx, app.AccountID, key)
	if errors.Is(err, state.ErrNotFound) {
		return baseKey(app.Runtime), nil
	}
	if err != nil {
		return "", fmt.Errorf("sched: runtime release lookup: %w", err)
	}
	if err := r.Validate(); err != nil {
		return "", fmt.Errorf("sched: invalid runtime binding: %w", err)
	}
	if r.Runtime != app.Runtime || r.Architecture != runtime.GOARCH {
		return "", errors.New("sched: runtime release does not match workload runtime and architecture")
	}
	return r.BaseKey(), nil
}
