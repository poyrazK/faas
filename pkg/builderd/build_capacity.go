package builderd

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrBuildDiskFull marks a builder node without room for a build drive. The
// build waits in the queue for capacity instead of failing its deployment
// (ADR-20261028090000).
var ErrBuildDiskFull = errors.New("builderd: insufficient host free space for build drive")

// buildDiskCapacityRetryDelay is a variable so tests need not wait.
var buildDiskCapacityRetryDelay = api.BuildDiskCapacityRetryDelay

// reclaimDependencyCaches frees up to need bytes of the developer dependency
// cache under driveDir, oldest entries first. An entry only saves a
// dependency install, so it yields to a build that needs the disk.
func reclaimDependencyCaches(driveDir string, need int64, now time.Time) {
	total, err := dependencyCacheSize(filepath.Join(driveDir, devDependencyCacheDir))
	if err != nil || total <= 0 || need <= 0 {
		return
	}
	// sweepDependencyCachesWithLimit treats a limit of zero as unlimited.
	_ = sweepDependencyCachesWithLimit(driveDir, now, max(total-need, 1))
}

// requeueForBuildCapacity returns a build the node had no disk for to the
// queue after a short wait. Its deployment stays in flight; another builder,
// or this one once space frees, takes it. It reports whether the claim went
// back to the queue.
func (b *Builderd) requeueForBuildCapacity(ctx context.Context, claim state.Build, cause error) bool {
	b.log.Warn("builderd: no build disk capacity; returning the build to the queue",
		"build", claim.ID, "deployment", claim.DeploymentID, "err", cause)
	b.emitBuildLog(ctx, claim.ID, "waiting for build capacity on the builder\n")
	timer := time.NewTimer(buildDiskCapacityRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// The worker's cancellation path requeues the claim.
		return false
	case <-timer.C:
	}
	if err := b.requeueClaim(ctx, claim, "build_disk_full"); err != nil {
		b.log.Warn("builderd: requeue build after disk shortfall", "build", claim.ID, "err", err)
		return false
	}
	return true
}
