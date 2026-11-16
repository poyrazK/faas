//go:build linux

package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// reapOne reaps guest-init's exited children that nothing else waits for:
// orphans re-parented to PID 1 and processes started without os/exec. It
// leaves a child alone while guest-init holds a pidfd for it. os/exec starts
// every child with a pidfd and its Wait collects the exit through that pidfd,
// so reaping the child first failed the Wait with ECHILD and lost the exit
// status: an essential companion whose command exited 127 crash-looped as
// "waitid: no child processes" (Hunt 34 H34-35). Both the single-workload
// signal loop and runWorkloads call this on SIGCHLD; the single-workload loop
// also calls it on a 100 ms tick.
func reapOne(log *slog.Logger) {
	if !childMayHaveExited() {
		return
	}
	reapUnwaitedChildren(log, "/proc", os.Getpid())
}

// childMayHaveExited peeks, without reaping, whether a child is a zombie. It
// is the steady-state fast path: one syscall and no /proc reads when no child
// has exited. A kernel that refuses the peek falls through to the scan.
func childMayHaveExited() bool {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_ALL, 0, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	if errors.Is(err, unix.ECHILD) {
		return false
	}
	return err != nil || info.Signo != 0
}

// reapUnwaitedChildren reaps every zombie child of self that no pidfd held by
// this process refers to.
func reapUnwaitedChildren(log *slog.Logger, procRoot string, self int) {
	zombies := zombieChildren(procRoot, self)
	if len(zombies) == 0 {
		return
	}
	waited := pidfdTargets(filepath.Join(procRoot, "self", "fdinfo"))
	for _, pid := range zombies {
		if waited[pid] {
			continue
		}
		var status syscall.WaitStatus
		got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if err != nil {
			// ECHILD: its owner collected it between the scan and here.
			if !errors.Is(err, syscall.ECHILD) && log != nil {
				log.Warn("guest-init: reap failed", "pid", pid, "err", err)
			}
			continue
		}
		if got > 0 && log != nil {
			log.Debug("guest-init: reaped child",
				"pid", got,
				"exited", status.Exited(),
				"signaled", status.Signaled(),
				"signal", status.Signal(),
				"exit_status", status.ExitStatus())
		}
	}
}

// zombieChildren lists the processes under procRoot whose state is Z and
// whose parent is ppid.
func zombieChildren(procRoot string, ppid int) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "stat"))
		if err != nil {
			continue // exited and reaped since the listing
		}
		state, parent, ok := parseProcStat(stat)
		if ok && state == "Z" && parent == ppid {
			pids = append(pids, pid)
		}
	}
	return pids
}

// parseProcStat returns the state and parent pid from /proc/<pid>/stat:
// "pid (comm) state ppid ...". comm may itself contain spaces and ")", so
// the fields start after the last ")".
func parseProcStat(stat []byte) (string, int, bool) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", 0, false
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 2 {
		return "", 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false
	}
	return fields[0], ppid, true
}

// pidfdTargets returns the pids that this process's pidfds refer to, read
// from the "Pid:" line pidfd fdinfo carries. A pidfd whose process is gone
// reports -1.
func pidfdTargets(fdinfoDir string) map[int]bool {
	entries, err := os.ReadDir(fdinfoDir)
	if err != nil {
		return nil
	}
	targets := make(map[int]bool)
	for _, entry := range entries {
		info, err := os.ReadFile(filepath.Join(fdinfoDir, entry.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(info), "\n") {
			value, ok := strings.CutPrefix(line, "Pid:")
			if !ok {
				continue
			}
			if pid, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && pid > 0 {
				targets[pid] = true
			}
			break
		}
	}
	return targets
}
