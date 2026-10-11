package state

import (
	"context"
	"time"
)

type RuntimeUpgradePublicEdgeWithdrawal struct {
	ID, RosterRevision string
	RuntimeUpgradePublicEdgeMember
	CreatedAt time.Time
}

type RuntimeUpgradePublicEdgeWithdrawalSnapshot struct {
	ID, FenceID   string
	Version       int64
	Closed, Known bool
	Active        int
}

type RuntimeUpgradePublicEdgeCoverageObservation struct {
	RuntimeUpgradePublicEdgeActivityObservation
	PendingWithdrawals []RuntimeUpgradePublicEdgeWithdrawal
}

// The callback installs the local irreversible fence and reads its tracker
// synchronously, with no database IO. Missing processes never synthesize a seal.
type RuntimeUpgradePublicEdgeWithdrawalStore interface {
	RepairRuntimeUpgradePublicEdgeWithdrawal(context.Context, RuntimeUpgradePublicEdgeMember, func(string) (RuntimeUpgradePublicEdgeWithdrawalSnapshot, error)) (bool, error)
}

type RuntimeUpgradePublicEdgeCoverageVerifier interface {
	ObserveRuntimeUpgradePublicEdgeCoverage(context.Context, string) (RuntimeUpgradePublicEdgeCoverageObservation, error)
}
