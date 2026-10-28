// adr: 20261027173012
package builderd

import (
	"context"
	"fmt"
)

// BuildDriveSizeBytes is the drive1 image size for builder VMs (M6). 28 GiB
// gives rootless native BuildKit enough room for Railpack's builder/runtime
// images, dependency layers, and the native local exporter; the previous
// 24 GiB budget exhausted while copying those layers. The produced app layer
// (in /build/out) is typically
// < 500 MB; the rest is transient build scratch.
const BuildDriveSizeBytes = 28 << 30

// BuildDriveMinWorkingSetBytes is the conservative host working-set budget for
// a Railpack solve. The ext4 image itself is sparse; requiring the full 28 GiB
// image size would reject healthy hosts even though the image only materialises
// the blocks BuildKit actually writes.
const BuildDriveMinWorkingSetBytes = 16 << 30

// BuildDriveHostReserveBytes keeps room for the host-side export directory,
// logs, and filesystem metadata. Without this preflight, a nearly-full host
// lets the guest run until virtio reports ENOSPC, which is misclassified as an
// opaque infrastructure timeout.
const BuildDriveHostReserveBytes = 4 << 30

// BuildDriveMinFreeBytes is the minimum filesystem space required before a
// builder drive is materialised.
const BuildDriveMinFreeBytes = BuildDriveMinWorkingSetBytes + BuildDriveHostReserveBytes

// BuildDriveSpaceReclaimer frees at least need bytes on the build drive
// filesystem, as far as it can. builderd wires the node artifact cache's
// oldest-first eviction (storage.ReclaimFreeBytes).
type BuildDriveSpaceReclaimer func(ctx context.Context, need int64) error

// reclaimBuildDriveSpace asks reclaim for room when the drive filesystem has
// less than BuildDriveMinFreeBytes free. The artifact cache sharing that disk
// only keeps api.ArtifactCacheMinFreeBytes free, so on a full node every
// cache-miss build was refused within a second (#5709). The capacity check
// that follows still decides whether the build runs.
func reclaimBuildDriveSpace(ctx context.Context, dir string, reclaim BuildDriveSpaceReclaimer, available func(string) (uint64, error)) error {
	if reclaim == nil {
		return nil
	}
	free, err := available(dir)
	if err != nil || free >= BuildDriveMinFreeBytes {
		return err
	}
	if err := reclaim(ctx, int64(BuildDriveMinFreeBytes)); err != nil {
		return fmt.Errorf("builderd: reclaim build drive space: %w", err)
	}
	return nil
}
