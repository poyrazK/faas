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
