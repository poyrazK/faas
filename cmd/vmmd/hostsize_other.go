//go:build !linux

package main

// hostMemTotalMB is unavailable off Linux; callers keep the single-box
// constants. Compute nodes are Linux-only.
func hostMemTotalMB() int { return 0 }

func hostCPUs() int { return 0 }
