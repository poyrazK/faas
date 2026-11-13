package sched

import "time"

// OOMBackoff exposes oomBackoff to sched_test (bug hunt H34).
type OOMBackoff = oomBackoff

func (b *oomBackoff) Record(deploymentID string, now time.Time) (bool, bool) {
	return b.record(deploymentID, now)
}

func (b *oomBackoff) Active(deploymentID string, now time.Time) (time.Duration, bool) {
	return b.active(deploymentID, now)
}

// ErrWorkloadOOMBackoff exposes errWorkloadOOMBackoff to sched_test.
var ErrWorkloadOOMBackoff = errWorkloadOOMBackoff
