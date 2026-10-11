package state

import (
	"context"
	"time"
)

// ProjectEnvironmentCloneMigrationHolding carries retained source-side handoff
// evidence. Lease is private cleanup authority and must not enter public output.
// SchedulingNodeID is catalog ownership, not original destination launch proof.
// Expiry, Pending=false and an instance state label do not establish closure.
type ProjectEnvironmentCloneMigrationHolding struct {
	Lease                                         MigrationLease `json:"-"`
	AppID, DeploymentID, WakeID, SchedulingNodeID string
}

type ProjectEnvironmentCloneMigrationRecoveryStore interface {
	ListProjectEnvironmentCloneMigrationHoldingsForRetirement(context.Context, ProjectEnvironmentCloneLease, string, int) ([]ProjectEnvironmentCloneMigrationHolding, error)
}

// ProjectEnvironmentCloneMigrationAdoption preserves destination dispatch
// ownership independently of current source metadata. Reading is not closure
// proof and never authorizes adoption replay or evidence release.
type ProjectEnvironmentCloneMigrationAdoption struct {
	DispatchID, AppID, DeploymentID, WakeID, RequestReason, DestinationNodeID string
	SourceLease                                                               MigrationLease `json:"-"`
	DispatchedAt                                                              time.Time
}

type ProjectEnvironmentCloneMigrationAdoptionRecoveryStore interface {
	ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(context.Context, ProjectEnvironmentCloneLease, string, int) ([]ProjectEnvironmentCloneMigrationAdoption, error)
}
