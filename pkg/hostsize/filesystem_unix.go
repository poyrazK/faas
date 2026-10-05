//go:build unix

package hostsize

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// FilesystemUsedPct reports how full the filesystem holding path is, the way
// df computes Use%: used / (used + available to unprivileged writers) * 100.
// Blocks reserved for root count as neither, so a filesystem whose
// unprivileged space is exhausted reads 100 even while root can still write.
//
// It measures the mounted filesystem rather than an LVM volume, so the same
// probe works whether /srv/fc is a logical volume, a partition or a bare
// cloud disk.
func FilesystemUsedPct(path string) (float64, error) {
	if path == "" {
		return 0, errors.New("hostsize: empty filesystem path")
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("hostsize: statfs %s: %w", path, err)
	}
	pct, err := usedPct(uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail))
	if err != nil {
		return 0, fmt.Errorf("hostsize: statfs %s: %w", path, err)
	}
	return pct, nil
}

// usedPct applies df's Use% formula to statfs block counts (all in the same
// unit): used / (used + available to unprivileged writers) * 100, where used
// is total minus free. Root-reserved blocks (free but not available) count as
// neither, so a filesystem with only the reserve left reads 100.
func usedPct(blocks, free, avail uint64) (float64, error) {
	if free > blocks {
		return 0, fmt.Errorf("filesystem reports %d free of %d blocks", free, blocks)
	}
	used := blocks - free
	if used+avail == 0 {
		return 0, errors.New("filesystem reports no usable blocks")
	}
	return 100 * float64(used) / float64(used+avail), nil
}
