//go:build linux

package internal

import (
	"os"
	"syscall"
)

// ProcessResourceUsage reads wait4's per-child resource counters. It is used
// only for one-shot function invocations; persistent workers cannot attribute
// process high-water RSS to a single request.
func ProcessResourceUsage(state *os.ProcessState) GuestProcessUsage {
	if state == nil {
		return GuestProcessUsage{}
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return GuestProcessUsage{}
	}
	cpuUsec := int64(usage.Utime.Sec)*1_000_000 + int64(usage.Utime.Usec) +
		int64(usage.Stime.Sec)*1_000_000 + int64(usage.Stime.Usec)
	if cpuUsec < 0 {
		cpuUsec = 0
	}
	peakRSSKiB := int64(usage.Maxrss)
	if peakRSSKiB < 0 {
		peakRSSKiB = 0
	}
	return GuestProcessUsage{
		CPUTimeMS: int((cpuUsec + 999) / 1_000),
		PeakRSSMB: int((peakRSSKiB + 1023) / 1024),
		Available: true,
	}
}
