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
	used := uint64(st.Blocks) - uint64(st.Bfree)
	avail := uint64(st.Bavail)
	if used+avail == 0 {
		return 0, fmt.Errorf("hostsize: statfs %s: filesystem reports no usable blocks", path)
	}
	return 100 * float64(used) / float64(used+avail), nil
}
