//go:build linux

package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// procVMStat holds the kernel's cumulative oom_kill counter.
const procVMStat = "/proc/vmstat"

// readVMStatOOMKills returns the kernel's cumulative OOM kill count, which
// counts kills by the global OOM killer as well as by memory cgroups.
func readVMStatOOMKills(path string) (uint64, bool) {
	//nolint:forbidigo // fixed procfs path inside the guest.
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), " ")
		if !ok || name != "oom_kill" {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// oomKilled reports whether a workload that exited with waitErr was killed
// by the kernel's OOM killer: SIGKILL while the kernel's OOM kill count rose
// during its lifetime (ADR-1155). It covers apps without a workload cgroup
// leaf, whose kills the per-leaf memory.events watcher never sees.
func oomKilled(waitErr error, before uint64, beforeOK bool, read func() (uint64, bool)) bool {
	var exitErr *exec.ExitError
	if !beforeOK || !errors.As(waitErr, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		return false
	}
	after, ok := read()
	return ok && after > before
}

// guestMemTotalMB is the guest's usable memory, reported as the peak of a
// global OOM kill: the kernel kills only once that is exhausted.
func guestMemTotalMB() int {
	stats, err := readMeminfo("/proc/meminfo")
	if err != nil || stats.MemTotal <= 0 {
		return 0
	}
	return int((stats.MemTotal + (1 << 20) - 1) >> 20)
}
