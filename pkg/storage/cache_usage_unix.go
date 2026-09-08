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
