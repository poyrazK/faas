//go:build linux

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// hostMemTotalMB reads MemTotal from /proc/meminfo, the kernel's own view of
// the machine. Returns 0 when it cannot be determined, which makes the caller
// keep the legacy single-box constants rather than size a node from a guess.
//
// os.ReadFile rather than os.Open: /proc/meminfo is a kernel pseudo-file of
// about 1.5 KiB, and the repo's forbidigo rule reserves os.Open for paths
// that have passed the customer-path symlink guard.
func hostMemTotalMB() int {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || !strings.EqualFold(fields[1], "kB") {
			return 0
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kb <= 0 {
			return 0
		}
		return int(kb / 1024)
	}
	return 0
}

// hostCPUs is the schedulable CPU count this vmmd can actually place work on.
func hostCPUs() int { return runtime.NumCPU() }
