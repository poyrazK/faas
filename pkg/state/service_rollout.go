package state

import "errors"

// IsServiceRollout identifies the internal marker used for a readiness-gated
// service deployment. A zero-step rollout is otherwise the legacy stable
// deployment shape, so the rolling_out state is the distinguishing bit.
func IsServiceRollout(d Deployment) bool {
	return d.CanaryTotalSteps == 0 && d.RolloutState == "rolling_out"
}

// ErrServiceRolloutInvalid is returned when a service rollout finalizer or
// aborter is called for a row that is not the active service-rollout marker.
var ErrServiceRolloutInvalid = errors.New("state: service rollout is not active")
