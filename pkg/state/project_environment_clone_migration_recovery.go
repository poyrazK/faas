package state

import "context"

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
