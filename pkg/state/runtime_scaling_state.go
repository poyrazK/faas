package state

import (
	"context"
	"time"
)

// RuntimeScalingState is operational history for one deployment's original
// environment. It is deliberately excluded from cloned configuration.
type RuntimeScalingState struct {
	RuntimeAppEnvSnapshot
	LastScaleInAt  *time.Time
	LastScaleOutAt *time.Time
}

type RuntimeScalingStateStore interface {
	RuntimeScalingStateForDeployment(context.Context, string, string, string) (RuntimeScalingState, error)
	StampDeploymentScaleIn(context.Context, string) error
	StampDeploymentScaleOut(context.Context, string) error
}

func runtimeScalingEnvironmentKey(scope, environmentID string) string {
	if environmentID != "" {
		return "environment:" + environmentID
	}
	return "scope:" + workloadEnvironmentSlug(scope)
}

func copyRuntimeScalingState(row RuntimeScalingState) RuntimeScalingState {
	if row.LastScaleInAt != nil {
		stamp := *row.LastScaleInAt
		row.LastScaleInAt = &stamp
	}
	if row.LastScaleOutAt != nil {
		stamp := *row.LastScaleOutAt
		row.LastScaleOutAt = &stamp
	}
	return row
}
