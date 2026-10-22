//go:build linux || darwin

package storage

import (
	"os"
	"syscall"
)

// cacheDiskUsage returns allocated filesystem bytes. Snapshot memory files are
// deliberately sparse: their logical size is the guest RAM limit, while their
// disk cost is only the non-zero pages. Eviction budgets protect cache disk
// capacity, so charging logical size would discard useful snapshots and app
// layers while most of the configured budget remained physically unused.
func cacheDiskUsage(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Blocks * 512
	}
	return info.Size()
}

// cacheDiskSpace returns the free (available to unprivileged writers) and
// total bytes of the filesystem holding path.
func cacheDiskSpace(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := int64(st.Bsize)
	return int64(st.Bavail) * bsize, int64(st.Blocks) * bsize, nil
}
