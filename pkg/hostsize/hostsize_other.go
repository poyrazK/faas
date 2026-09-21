//go:build !linux

package hostsize

// MemTotalMB is unavailable off Linux; callers keep the single-box
// constants. Compute nodes are Linux-only.
func MemTotalMB() int { return 0 }

// CPUs is unavailable off Linux for the same reason.
func CPUs() int { return 0 }
