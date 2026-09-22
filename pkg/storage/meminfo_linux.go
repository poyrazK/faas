//go:build linux

package storage

import (
	"os"
	"strconv"
	"strings"
)

// meminfoPath is a kernel-provided pseudo-file, never a customer-supplied
// path. It is read with os.ReadFile rather than os.Open both because the
// file is ~1.5 KiB and because the repo's forbidigo rule reserves os.Open
// for paths that have been through the symlink guard — a guard that has
// nothing to say about /proc.
const meminfoPath = "/proc/meminfo"

// hostMemTotalBytes reads MemTotal from /proc/meminfo. It returns 0 when the
// value cannot be determined, which makes the caller fall back to the flat
// default instead of sizing a cache budget from a number it does not have.
func hostMemTotalBytes() int64 {
	raw, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		// "MemTotal:       16369288 kB" — value then unit. The kernel has
		// always reported kB here; anything else is unexpected enough that
		// falling back beats guessing a scale factor.
		fields := strings.Fields(rest)
		if len(fields) != 2 || !strings.EqualFold(fields[1], "kB") {
			return 0
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kb <= 0 {
			return 0
		}
		return kb * 1024
	}
	return 0
}
