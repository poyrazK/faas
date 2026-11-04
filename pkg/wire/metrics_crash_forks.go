package wire

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// CrashForkMetrics are schedd's ADR-732 fork and ADR-733 crash capture
// metrics. Every method is nil-safe, so coordinators built without a
// registry (tests, degraded boots) record nothing.
type CrashForkMetrics struct {
	CapturesTotal      *prometheus.CounterVec
	CaptureSeconds     prometheus.Histogram
	ForkRestoresTotal  *prometheus.CounterVec
	ForkRestoreSeconds *prometheus.HistogramVec
	ForkClaimWait      *prometheus.HistogramVec
}

// Crash capture results: ready, or the failure code the row records.
var crashCaptureResults = []string{"ready", "capture_failed", "instance_gone", "storage_unsupported", "capture_timeout"}

// NewCrashForkMetrics registers the families on reg (nil registers nothing).
func NewCrashForkMetrics(reg prometheus.Registerer) *CrashForkMetrics {
	m := &CrashForkMetrics{
		CapturesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_crash_captures_total",
			Help: "ADR-733 crash captures that finished, by trigger (http_5xx, manual) and result (ready or the failure code).",
		}, []string{"trigger", "result"}),
		CaptureSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "schedd_crash_capture_duration_seconds",
			Help:    "Time to take a crash capture, successful captures only: the app lock wait plus the pause-snapshot-resume of the serving instance.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 300},
		}),
		ForkRestoresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_fork_restores_total",
			Help: "ADR-732 fork restores, by source (deployment, crash_capture) and result (running or the failure code).",
		}, []string{"source", "result"}),
		ForkRestoreSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "schedd_fork_restore_duration_seconds",
			Help:    "Time from claiming a fork to its instance running, by source.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"source"}),
		ForkClaimWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "schedd_fork_claim_wait_seconds",
			Help:    "Time a fork waited queued before a scheduler claimed it, by source. For crash_capture this includes imaged staging the plaintext.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300},
		}, []string{"source"}),
	}
	if reg == nil {
		return m
	}
	m.CapturesTotal = registerOrExisting(reg, m.CapturesTotal)
	m.CaptureSeconds = registerOrExisting(reg, m.CaptureSeconds)
	m.ForkRestoresTotal = registerOrExisting(reg, m.ForkRestoresTotal)
	m.ForkRestoreSeconds = registerOrExisting(reg, m.ForkRestoreSeconds)
	m.ForkClaimWait = registerOrExisting(reg, m.ForkClaimWait)
	for _, trigger := range []string{"http_5xx", "manual"} {
		for _, result := range crashCaptureResults {
			m.CapturesTotal.WithLabelValues(trigger, result)
		}
	}
	for _, source := range []string{"deployment", "crash_capture"} {
		m.ForkRestoresTotal.WithLabelValues(source, "running")
	}
	return m
}

// registerOrExisting registers c, or returns the collector already
// registered under the same descriptor (a rebuilt server on a kept registry).
func registerOrExisting[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	err := reg.Register(c)
	if err == nil {
		return c
	}
	var already prometheus.AlreadyRegisteredError
	if errors.As(err, &already) {
		if existing, ok := already.ExistingCollector.(C); ok {
			return existing
		}
	}
	panic(err)
}

// CrashCaptureFinished records one capture outcome.
func (m *CrashForkMetrics) CrashCaptureFinished(trigger, result string, paused time.Duration) {
	if m == nil {
		return
	}
	m.CapturesTotal.WithLabelValues(trigger, result).Inc()
	if result == "ready" {
		m.CaptureSeconds.Observe(paused.Seconds())
	}
}

// ForkClaimed records how long a fork waited queued.
func (m *CrashForkMetrics) ForkClaimed(source string, waited time.Duration) {
	if m == nil {
		return
	}
	m.ForkClaimWait.WithLabelValues(source).Observe(max(0, waited.Seconds()))
}

// ForkRestoreFinished records one restore outcome.
func (m *CrashForkMetrics) ForkRestoreFinished(source, result string, took time.Duration) {
	if m == nil {
		return
	}
	m.ForkRestoresTotal.WithLabelValues(source, result).Inc()
	if result == "running" {
		m.ForkRestoreSeconds.WithLabelValues(source).Observe(took.Seconds())
	}
}
