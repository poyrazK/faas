package state

import (
	"context"
	"time"
)

// ProjectEnvironmentCloneWakeDispatch is original scheduler dispatch ownership,
// not a native launch, producer-closure receipt or permission to redispatch.
type ProjectEnvironmentCloneWakeDispatch struct {
	InstanceID, AppID, DeploymentID, NodeID, WakeID string
	RequestReason, Method                           string
	DispatchedAt                                    time.Time
}

// Recovery is private and authenticated under the current compensation lease.
// Pages use original UUID order and survive removal of instance metadata.
type ProjectEnvironmentCloneWakeRecoveryStore interface {
	ListProjectEnvironmentCloneWakeDispatchesForRetirement(context.Context, ProjectEnvironmentCloneLease, string, int) ([]ProjectEnvironmentCloneWakeDispatch, error)
}
