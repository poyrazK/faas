package sched

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Park after a baseline health probe (ADR-20261116120000 §3). An instance
// that only a platform health probe woke parks once the probe is done
// instead of waiting out its idle timeout: the platform pays for the wake,
// so it should last seconds, not the app's idle timeout. Park reuses the
// node's init snapshot when it can (captureInitOrReuse), so this is a
// destroy, not a capture. A customer request after the probe moves the
// instance's last request past the probe's end, and then the ordinary idle
// timeout applies again.

// probeOnly reports a RUNNING instance whose latest activity is the
// unbilled health probe that woke it.
func (in InstanceInfo) probeOnly() bool {
	if in.ProbeWakeDoneAt.IsZero() || in.State != state.StateRunning {
		return false
	}
	last := idleReference(in)
	return !last.IsZero() && !last.After(in.ProbeWakeDoneAt.Add(api.ProtectionProbeActivitySlack))
}

// probeOnlyIdle reports a probe-only instance whose park grace has passed.
func (in InstanceInfo) probeOnlyIdle(now time.Time) bool {
	return in.probeOnly() && now.Sub(in.ProbeWakeDoneAt) >= api.ProtectionProbeParkGrace
}
