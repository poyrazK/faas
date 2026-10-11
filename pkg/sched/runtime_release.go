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
