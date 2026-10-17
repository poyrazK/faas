package builderd

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ReaperMetrics describes this process's recovery loop, never fleet backlog.
// It uses fixed outcome/operation labels and no build, tenant, or error labels.
type ReaperMetrics struct {
	lastAttempt, lastSuccess, lastCompleted, lastResult prometheus.Gauge
	passes                                              *prometheus.CounterVec
	swept                                               prometheus.Counter
	cleanup                                             *prometheus.CounterVec
	now                                                 func() time.Time
}

func NewReaperMetrics(reg prometheus.Registerer, interval, threshold time.Duration) *ReaperMetrics {
	gauge := func(name, help string) prometheus.Gauge {
		g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
		reg.MustRegister(g)
		return g
	}
	m := &ReaperMetrics{
		lastAttempt:   gauge("builderd_build_reaper_last_attempt_timestamp_seconds", "Start of the latest reaper pass in Unix seconds; zero until the first scheduled pass."),
		lastSuccess:   gauge("builderd_build_reaper_last_success_timestamp_seconds", "Completion of the last pass with successful sweep, cleanup claim, VM cancellation and result recording. Zero until success."),
		lastCompleted: gauge("builderd_build_reaper_last_completed_timestamp_seconds", "Completion time of the latest reaper pass in Unix seconds; zero until completion."),
		lastResult:    gauge("builderd_build_reaper_last_pass_success", "Whether the last completed whole pass succeeded. Zero before first completion; use last_completed_timestamp_seconds to distinguish startup."),
		passes:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "builderd_build_reaper_passes_total", Help: "Completed reaper passes by outcome (success or error). Cleanup errors count even if the row sweep succeeded."}, []string{"outcome"}),
		swept:         prometheus.NewCounter(prometheus.CounterOpts{Name: "builderd_build_reaper_recovered_builds_total", Help: "Running build rows atomically failed by this process's reaper; not VM cleanup completions."}),
		cleanup:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "builderd_build_vm_cleanup_operations_total", Help: "VM cancel and durable result-record calls by operation (cancel or record) and response outcome (success or error). A successful record call may be a fenced no-op."}, []string{"operation", "outcome"}),
		now:           time.Now,
	}
	reg.MustRegister(m.passes, m.swept, m.cleanup)
	for _, outcome := range []string{"success", "error"} {
		m.passes.WithLabelValues(outcome)
		for _, op := range []string{"cancel", "record"} {
			m.cleanup.WithLabelValues(op, outcome)
		}
	}
	gauge("builderd_build_reaper_started_timestamp_seconds", "Recovery loop initialization time in Unix seconds, used for the first-pass grace period.").Set(float64(time.Now().Unix()))
	gauge("builderd_build_reaper_interval_seconds", "Effective configured reaper sweep interval in seconds.").Set(interval.Seconds())
	gauge("builderd_build_reaper_threshold_seconds", "Effective normalized running-build recovery threshold in seconds.").Set(threshold.Seconds())
	// A pass can perform 64 serial VM stops plus 64 result writes, each with
	// its existing 30-second timeout. Do not alert as stalled during that work.
	gauge("builderd_build_reaper_cleanup_budget_seconds", "Maximum serial cleanup call budget per pass (batch size times cancel and record timeouts); excludes the store sweep and claim queries.").Set((2 * builderVMCleanupBatchSize * reaperVMCancelTimeout).Seconds())
	return m
}

func (m *ReaperMetrics) begin() {
	if m != nil {
		m.lastAttempt.Set(float64(m.now().UnixNano()) / float64(time.Second))
	}
}

func (m *ReaperMetrics) finish(result reaperPassResult) {
	if m == nil {
		return
	}
	m.swept.Add(float64(result.swept))
	m.lastCompleted.Set(float64(m.now().UnixNano()) / float64(time.Second))
	m.lastResult.Set(0)
	outcome := "error"
	if result.err == nil && !result.cleanupFailed {
		outcome = "success"
		m.lastResult.Set(1)
		m.lastSuccess.Set(float64(m.now().UnixNano()) / float64(time.Second))
	}
	m.passes.WithLabelValues(outcome).Inc()
}

func (m *ReaperMetrics) observeCleanup(operation string, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.cleanup.WithLabelValues(operation, outcome).Inc()
}
