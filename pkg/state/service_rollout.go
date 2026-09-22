package state

import (
	"errors"
	"time"
)

const (
	ServiceRolloutActionPromote = "promote"
	ServiceRolloutActionAbort   = "abort"

	ServiceRolloutPhasePending  = "pending"
	ServiceRolloutPhaseRouting  = "routing"
	ServiceRolloutPhaseDraining = "draining"
	ServiceRolloutPhaseComplete = "complete"
)

// ServiceRolloutHandoff is the durable, bounded coordination record for a
// zero-step service rollout. It deliberately lives on the deployment row:
// rollout_state='rolling_out' selects recovery work, while this payload tells
// a replacement scheduler which direction to resume and which barrier last
// completed. Gateway identities are node names from the bounded compute-node
// registry, never request-derived labels.
type ServiceRolloutHandoff struct {
	Action                  string     `json:"action,omitempty"`
	Phase                   string     `json:"phase,omitempty"`
	PredecessorDeploymentID string     `json:"predecessor_deployment_id,omitempty"`
	Generation              int64      `json:"generation,omitempty"`
	ExpectedGateways        []string   `json:"expected_gateways,omitempty"`
	AcknowledgedGateways    []string   `json:"acknowledged_gateways,omitempty"`
	MissingGateways         []string   `json:"missing_gateways,omitempty"`
	RetryCount              int        `json:"retry_count,omitempty"`
	LastError               string     `json:"last_error,omitempty"`
	Reason                  string     `json:"reason,omitempty"`
	StartedAt               *time.Time `json:"started_at,omitempty"`
	UpdatedAt               *time.Time `json:"updated_at,omitempty"`
	AcknowledgedAt          *time.Time `json:"acknowledged_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
}

func (h ServiceRolloutHandoff) ActiveAbort() bool {
	return h.Action == ServiceRolloutActionAbort && h.Phase != ServiceRolloutPhaseComplete
}

// A scheduler may finish a route-ACK wait after an operator has requested an
// abort. Such stale progress must not replace the abort intent or a newer
// routing generation.
func serviceRolloutHandoffCanReplace(current, next ServiceRolloutHandoff) bool {
	if current.ActiveAbort() && next.Action != ServiceRolloutActionAbort {
		return false
	}
	return current.Action != next.Action || next.Generation >= current.Generation
}

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
