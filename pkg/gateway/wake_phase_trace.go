package gateway

import (
	"context"
	"sync"
	"time"
)

// wakePhaseTrace is a request-local timing carrier for the platform-only cold
// path. It crosses gateway helpers through context values but never crosses a
// process boundary or becomes a metric label.
type wakePhaseTrace struct {
	mu sync.Mutex

	platformStarted   time.Time
	admissionStarted  time.Time
	schedulerComplete time.Time
	targetPublished   time.Time
	proxyStarted      time.Time
}

type wakePhaseTraceKey struct{}

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
	t.mu.Lock()
	platformStarted := t.platformStarted
	admissionStarted := t.admissionStarted
	schedulerComplete := t.schedulerComplete
	targetPublished := t.targetPublished
	proxyStarted := t.proxyStarted
	t.mu.Unlock()

	observeWakePhaseBetween(metrics, "pre_admission", platformStarted, admissionStarted)
	observeWakePhaseBetween(metrics, "scheduler_wake", admissionStarted, schedulerComplete)
	observeWakePhaseBetween(metrics, "target_publication", schedulerComplete, targetPublished)
	observeWakePhaseBetween(metrics, "post_publication", targetPublished, proxyStarted)
	observeWakePhaseBetween(metrics, "internal_proxy", proxyStarted, firstByte)
}

func observeWakePhaseBetween(metrics *Metrics, phase string, start, end time.Time) {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return
	}
	metrics.ObserveWakePhase(phase, end.Sub(start))
}
