package state

import "errors"

// NormalizeRolloutState returns the canonical state for a deployment row.
// The rollout_state column is NOT NULL on current schemas, but older rows and
// in-memory fixtures may still carry the zero value. Treating it as pending at
// every state boundary keeps reads, orchestrator selection, and recovery
// guards on one vocabulary instead of exposing an empty state to callers.
func NormalizeRolloutState(rolloutState string) string {
	if rolloutState == "" {
		return "pending"
	}
	return rolloutState
}

// IsServiceRollout identifies the internal marker used for a readiness-gated
// service deployment. A zero-step rollout is otherwise the legacy stable
// deployment shape, so the rolling_out state is the distinguishing bit.
func IsServiceRollout(d Deployment) bool {
	return d.CanaryTotalSteps == 0 && NormalizeRolloutState(d.RolloutState) == "rolling_out"
}

// normalizedDeploymentScope is the state-layer equivalent of the database's
// coalesce(nullif(scope, blank), 'default') write rule. MemStore tests and legacy
// fixtures can still contain an empty scope, so readers that compare scopes
// must collapse both representations to the same logical environment.
func normalizedDeploymentScope(scope string) string {
	if scope == "" {
		return DefaultEnvScope
	}
	return scope
}

// ErrServiceRolloutInvalid is returned when a service rollout finalizer or
// aborter is called for a row that is not the active service-rollout marker.
var ErrServiceRolloutInvalid = errors.New("state: service rollout is not active")
