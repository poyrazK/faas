package fcvm

import (
	"log/slog"
	"time"
)

// captureTimer attributes one snapshot capture to its consecutive phases.
// Production parks spent 7.1 s at p50 and 12.4 s at p90 between schedd's
// wake.park_started and wake.park_completed with no breakdown of the vmmd
// share (2026-11-17), so a capture logs where its time went. Each mark
// records the time since the previous one, so the phases sum to total_ms.
type captureTimer struct {
	start, last time.Time
	phases      []any
	now         func() time.Time
}

func newCaptureTimer(now func() time.Time) *captureTimer {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &captureTimer{start: t, last: t, now: now}
}

// mark closes the phase that ended now.
func (t *captureTimer) mark(phase string) {
	n := t.now()
	t.phases = append(t.phases, phase+"_ms", n.Sub(t.last).Milliseconds())
	t.last = n
}

// attrs returns the recorded phases and the total as slog key/value pairs.
func (t *captureTimer) attrs() []any {
	return append(append([]any(nil), t.phases...), "total_ms", t.now().Sub(t.start).Milliseconds())
}

func logSnapshotCaptureTiming(instance, key string, resumed bool, memBytes, driveWritten int64, t *captureTimer) {
	slog.Default().Info("vmm: snapshot capture timing", append([]any{
		"instance", instance, "key", key, "resume_before_publish", resumed,
		"mem_bytes", memBytes, "drive_written_bytes", driveWritten,
	}, t.attrs()...)...)
}
