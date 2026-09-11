package state

import (
	"context"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OpenAPICaptureFn projects the edge-rule set visible at a deployment's
// live transition into a canonical OpenAPI snapshot. The Store invokes the
// callback with the in-flight transaction for Postgres (or nil for MemStore)
// and persists the returned snapshot before committing the transition.
//
// A zero-value OpenAPISnapshot with a nil error means capture is not wired.
// This keeps non-apid state tools and tests backwards-compatible while the
// production apid process registers the real projector at startup.
type OpenAPICaptureFn func(ctx context.Context, db sqlc.DBTX, deploymentID, appID, scope string, rules []api.CreateEdgeRuleRequest) (OpenAPISnapshot, error)

var (
	openAPICaptureMu sync.RWMutex
	openAPICapture   OpenAPICaptureFn = noopOpenAPICapture
)

// RegisterOpenAPICapture installs the process-wide snapshot projector. Passing
// nil restores the no-op implementation, which is useful for isolated tests.
func RegisterOpenAPICapture(fn OpenAPICaptureFn) {
	openAPICaptureMu.Lock()
	defer openAPICaptureMu.Unlock()
	if fn == nil {
		openAPICapture = noopOpenAPICapture
		return
	}
	openAPICapture = fn
}

func getOpenAPICapture() OpenAPICaptureFn {
	openAPICaptureMu.RLock()
	defer openAPICaptureMu.RUnlock()
	return openAPICapture
}

func noopOpenAPICapture(context.Context, sqlc.DBTX, string, string, string, []api.CreateEdgeRuleRequest) (OpenAPISnapshot, error) {
	return OpenAPISnapshot{}, nil
}
