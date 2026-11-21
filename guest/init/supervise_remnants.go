package main

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A requested restart (a developer live patch, ADR-740, or a secret reload)
// signals only the workload's direct child. When that child is a shell or a
// package manager running the real server (`/bin/sh -c node server.js`,
// `npm run start`), the shell exits and the server lives on, orphaned, still
// holding the app's port. The replacement then fails with EADDRINUSE until
// the restart budget is spent and the instance dies (prod, 2026-11-16).
//
// Every descendant stays in the main workload's cgroup leaf, so before the
// replacement starts the supervisor empties that leaf: SIGTERM first, then
// SIGKILL once the grace has passed.

const (
	remnantTermGrace = 2 * time.Second
	remnantKillWait  = 3 * time.Second
	remnantPoll      = 20 * time.Millisecond
)

// remnantReaper empties one cgroup leaf. Its I/O is injectable for tests.
type remnantReaper struct {
	readProcs func(leaf string) ([]byte, error)
	kill      func(pid int, sig syscall.Signal) error
	now       func() time.Time
	sleep     func(time.Duration)
	self      int
}

// reap signals every process left in leaf and waits until it is empty. It
// refuses any path that is not a direct child of the cgroup root, so it can
// never signal guest-init or another workload's leaf.
func (r remnantReaper) reap(root, leaf string) (int, error) {
	if leaf == "" || filepath.Clean(leaf) == filepath.Clean(root) || filepath.Dir(filepath.Clean(leaf)) != filepath.Clean(root) {
		return 0, nil
	}
	signalled := map[int]bool{}
	start := r.now()
	sig := syscall.SIGTERM
	for {
		raw, err := r.readProcs(leaf)
		if errors.Is(err, fs.ErrNotExist) {
			return len(signalled), nil
		}
		if err != nil {
			return len(signalled), fmt.Errorf("read %s: %w", filepath.Join(leaf, "cgroup.procs"), err)
		}
		pids := parseCgroupProcs(raw, r.self)
		if len(pids) == 0 {
			return len(signalled), nil
		}
		elapsed := r.now().Sub(start)
		if elapsed >= remnantTermGrace+remnantKillWait {
			return len(signalled), fmt.Errorf("%d process(es) still in %s after SIGKILL", len(pids), leaf)
		}
		if elapsed >= remnantTermGrace && sig != syscall.SIGKILL {
			sig = syscall.SIGKILL
			clear(signalled)
		}
		for _, pid := range pids {
			if !signalled[pid] {
				_ = r.kill(pid, sig) // an exited pid is already what we want
				signalled[pid] = true
			}
		}
		r.sleep(remnantPoll)
	}
}

func parseCgroupProcs(raw []byte, self int) []int {
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 1 || pid == self {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}
