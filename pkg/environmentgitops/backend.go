package environmentgitops

import (
	"context"

	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// IntentBackend exercises customer-intent semantics in core acceptance. It
// does not prove serving-fleet or guest convergence. Production apid wiring
// must use its backend with durable post-commit effect recovery and runtime
// verification, rather than treating this adapter as a full environment loop.
type IntentBackend struct {
	Store state.EnvironmentGitOpsIntentStore
}

func (b IntentBackend) Observe(ctx context.Context, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) (Observation, error) {
	return b.Store.ObserveEnvironmentGitOps(ctx, lease, desired)
}

func (b IntentBackend) Apply(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) ([]Step, error) {
	return b.Store.ApplyEnvironmentGitOps(ctx, lease, plan)
}
