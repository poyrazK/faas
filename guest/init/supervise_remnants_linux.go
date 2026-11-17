//go:build linux

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// reapRestartRemnants empties the main workload's cgroup leaf after a
// requested restart stopped its direct child, so no orphaned descendant
// keeps the app's port from the replacement (see supervise_remnants.go).
func (s *Supervisor) reapRestartRemnants() {
	leaf := s.WorkloadLeaf()
	if leaf == "" {
		return // the root cgroup: guest-init itself lives there
	}
	reaper := remnantReaper{
		readProcs: func(leaf string) ([]byte, error) { return os.ReadFile(filepath.Join(leaf, "cgroup.procs")) },
		kill:      syscall.Kill,
		now:       time.Now,
		sleep:     time.Sleep,
		self:      os.Getpid(),
	}
	signalled, err := reaper.reap(cgroupRoot, leaf)
	if err != nil {
		slog.Warn("supervisor: restart left processes behind", "leaf", leaf, "err", err)
		return
	}
	if signalled > 0 {
		slog.Info("supervisor: stopped processes the restarted workload left behind", "leaf", leaf, "processes", signalled)
	}
}
