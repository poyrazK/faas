//go:build linux

package storage

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// hostMemTotalBytes reads MemTotal from /proc/meminfo. Returns 0 when it
// cannot be determined, which makes the caller fall back to the flat default
// rather than guess a budget from a number it does not have.
func hostMemTotalBytes() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 1 {
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
