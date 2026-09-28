package gateway

import (
	"context"
	"sync"
	"time"
)

// wakePhaseTrace is a request-local timing carrier for the platform-only cold
// path. Only its bounded phase values are copied into a per-wake event; the
// trace itself stays in gateway context and never becomes a metric label.
type wakePhaseTrace struct {
	mu sync.Mutex

	platformStarted   time.Time
	admissionStarted  time.Time
	schedulerComplete time.Time
	targetPublished   time.Time
	proxyStarted      time.Time
}

type wakePhaseTraceKey struct{}

type wakePhaseMeasurement struct {
	phase    string
	duration time.Duration
	observed bool
}

func newWakePhaseTrace(start time.Time) *wakePhaseTrace {
	return &wakePhaseTrace{platformStarted: start}
}

func withWakePhaseTrace(ctx context.Context, trace *wakePhaseTrace) context.Context {
	if trace == nil {
		return ctx
	}
	return context.WithValue(ctx, wakePhaseTraceKey{}, trace)
}

func wakePhaseTraceFrom(ctx context.Context) *wakePhaseTrace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(wakePhaseTraceKey{}).(*wakePhaseTrace)
	return trace
}

func markWakeAdmissionStarted(ctx context.Context) {
	if trace := wakePhaseTraceFrom(ctx); trace != nil {
		trace.mu.Lock()
		if trace.admissionStarted.IsZero() {
			trace.admissionStarted = time.Now()
		}
		trace.mu.Unlock()
	}
}

func markWakeSchedulerComplete(ctx context.Context) {
	if trace := wakePhaseTraceFrom(ctx); trace != nil {
		trace.mu.Lock()
		if trace.schedulerComplete.IsZero() {
			trace.schedulerComplete = time.Now()
		}
		trace.mu.Unlock()
	}
}

func markWakeTargetPublished(ctx context.Context) {
	if trace := wakePhaseTraceFrom(ctx); trace != nil {
		trace.mu.Lock()
		if trace.targetPublished.IsZero() {
			trace.targetPublished = time.Now()
		}
		trace.mu.Unlock()
	}
}

func (t *wakePhaseTrace) markProxyStarted(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.proxyStarted.IsZero() {
		t.proxyStarted = at
	}
	t.mu.Unlock()
}

func (t *wakePhaseTrace) observe(metrics *Metrics, firstByte time.Time) {
	if t == nil || metrics == nil {
		return
	}
	for _, phase := range t.measurements(firstByte) {
		if phase.observed {
			metrics.ObserveWakePhase(phase.phase, phase.duration)
		}
	}
}

// gatewayPhasesMS returns the request-local phase boundaries for the
// per-wake proxy_first_byte event. The event already has a wake ID, so these
// values let an operator correlate gateway dispatch and proxy delay with the
// scheduler and VMMD events without adding high-cardinality metric labels.
func (t *wakePhaseTrace) gatewayPhasesMS(firstByte time.Time) map[string]int64 {
	if t == nil {
		return nil
	}
	var phases map[string]int64
	for _, phase := range t.measurements(firstByte) {
		if !phase.observed {
			continue
		}
		if phases == nil {
			phases = make(map[string]int64, 5)
		}
		phases[phase.phase] = phase.duration.Milliseconds()
	}
	return phases
}

func (t *wakePhaseTrace) measurements(firstByte time.Time) [5]wakePhaseMeasurement {
	t.mu.Lock()
	platformStarted := t.platformStarted
	admissionStarted := t.admissionStarted
	schedulerComplete := t.schedulerComplete
	targetPublished := t.targetPublished
	proxyStarted := t.proxyStarted
	t.mu.Unlock()

	return [5]wakePhaseMeasurement{
		wakePhaseBetween("pre_admission", platformStarted, admissionStarted),
		wakePhaseBetween("scheduler_wake", admissionStarted, schedulerComplete),
		wakePhaseBetween("target_publication", schedulerComplete, targetPublished),
		wakePhaseBetween("post_publication", targetPublished, proxyStarted),
		wakePhaseBetween("internal_proxy", proxyStarted, firstByte),
	}
}

func wakePhaseBetween(phase string, start, end time.Time) wakePhaseMeasurement {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return wakePhaseMeasurement{}
	}
	return wakePhaseMeasurement{phase: phase, duration: end.Sub(start), observed: true}
}
