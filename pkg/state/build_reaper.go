package state

import (
	"context"
	"time"
)

// StuckBuildSweepStore is the optional store capability used by builderd's
// VM-aware reaper. It preserves the existing SweepStuckRunningBuilds API for
// callers that only need a count, while allowing builderd to learn exactly
// which claims it failed so it can terminate their builder VMs.
//
// Implementations must return only the build IDs that were atomically changed
// from running to failed by this call. A concurrent completion or requeue must
// therefore not be returned as a stale cancellation target.
type StuckBuildSweepStore interface {
	SweepStuckRunningBuildsWithIDs(ctx context.Context, threshold time.Time) ([]string, error)
}

// BuildClaimRecoveryStore is the optional claim-fenced recovery capability
// used by builderd when a worker must return a claim to the queue. The
// started_at fence prevents a stale worker from requeueing a newer claim for
// the same build after a timeout, reaper pass, or transient read failure.
type BuildClaimRecoveryStore interface {
	RequeueBuildIfClaim(ctx context.Context, claim Build) error
}

// BuildVMCleanupClaim is one durable builder-VM teardown obligation claimed
// by a builderd worker. The token prevents a late result from an expired
// claim from completing a newer worker's attempt.
type BuildVMCleanupClaim struct {
	BuildID    string
	ClaimToken string
}

// BuildVMCleanupStore is the optional durable retry queue for builder-VM
// teardown. State transitions that can terminate a running build enqueue a
// row in the same transaction as the build flip. Builderd claims due rows,
// asks vmmd to stop the corresponding VM, and records either completion or a
// retryable error.
type BuildVMCleanupStore interface {
	ClaimBuildVMCleanup(ctx context.Context, limit int) ([]BuildVMCleanupClaim, error)
	CompleteBuildVMCleanup(ctx context.Context, buildID, claimToken string, cleanupErr error) error
}
