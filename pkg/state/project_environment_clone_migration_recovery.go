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

// ProjectEnvironmentCloneMigrationResume preserves original source restart
// uncertainty. Reading never authorizes resume replay or evidence release.
type ProjectEnvironmentCloneMigrationResume struct {
	DispatchID, AppID, DeploymentID, WakeID, RequestReason string
	SourceLease                                            ProjectEnvironmentCloneMigrationResumeSource `json:"-"`
	DispatchedAt                                           time.Time
}

type ProjectEnvironmentCloneMigrationResumeRecoveryStore interface {
	ListProjectEnvironmentCloneMigrationResumesForRetirement(context.Context, ProjectEnvironmentCloneLease, string, int) ([]ProjectEnvironmentCloneMigrationResume, error)
}

// ProjectEnvironmentCloneMigrationResumeSource records only original source
// identity. It makes no claim about snapshot completion or current lease state.
type ProjectEnvironmentCloneMigrationResumeSource struct {
	InstanceID, LeaseToken, SourceNodeID string
	CreatedAt, LeaseExpiresAt            time.Time
}
