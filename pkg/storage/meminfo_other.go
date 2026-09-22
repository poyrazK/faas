//go:build !linux

package storage

// hostMemTotalBytes is unavailable off Linux; callers fall back to the flat
// default. Compute nodes are Linux-only.
func hostMemTotalBytes() int64 { return 0 }
