//go:build !linux

package main

// reapRestartRemnants is Linux-only: the workload's cgroup leaf exists only
// inside the guest.
func (s *Supervisor) reapRestartRemnants() {}
