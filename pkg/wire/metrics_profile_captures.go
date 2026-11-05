package wire

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ProfileCaptureMetrics are schedd's on-demand profile capture metrics
// (ADR-1155). Every method is nil-safe, so engines built without a registry
// (tests, degraded boots) record nothing.
type ProfileCaptureMetrics struct {
	Finished  *prometheus.CounterVec
	Duration  *prometheus.HistogramVec
	QueueWait *prometheus.HistogramVec
	Skipped   *prometheus.CounterVec
}

// ProfileCaptureTriggers are the capture triggers: manual, or the automatic
// trigger that queued it.
var ProfileCaptureTriggers = []string{"manual", "memory_pressure", "slow_request", "cpu_saturation"}

// ProfileCaptureResults are the outcomes a finished capture records: ready,
// or why it failed.
var ProfileCaptureResults = []string{"ready", "no_running_instance", "instance_stopped", "guest_refused", "timeout", "unreachable", "not_stored", "gone"}

// ProfileCaptureSkips are the reasons schedd did not queue an automatic capture.
var ProfileCaptureSkips = []string{"not_running", "plan", "not_released", "opted_out", "interval", "active", "quota", "storage_share", "storage_full", "unavailable"}

// NewProfileCaptureMetrics registers the families on reg (nil registers nothing).
func NewProfileCaptureMetrics(reg prometheus.Registerer) *ProfileCaptureMetrics {
	m := &ProfileCaptureMetrics{
		Finished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_profile_captures_total",
			Help: "ADR-1155 on-demand profile captures that finished, by trigger (manual or the automatic trigger) and result (ready or why it failed).",
		}, []string{"trigger", "result"}),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "schedd_profile_capture_duration_seconds",
			Help:    "Time from claiming a profile capture to storing its result, by trigger: the window, the guest's flush grace and the store write.",
			Buckets: []float64{1, 2, 5, 10, 15, 30, 45, 60, 75, 90, 120},
		}, []string{"trigger"}),
		QueueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "schedd_profile_capture_queue_wait_seconds",
			Help:    "Time a profile capture waited queued before a scheduler claimed it, by trigger. Manual captures are claimed first.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		}, []string{"trigger"}),
		Skipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_profile_capture_automatic_skipped_total",
			Help: "Automatic profile captures schedd did not queue, by trigger and reason (rate limit, quota, storage, release, opt-out).",
		}, []string{"trigger", "reason"}),
	}
	if reg == nil {
		return m
	}
	m.Finished = registerOrExisting(reg, m.Finished)
	m.Duration = registerOrExisting(reg, m.Duration)
	m.QueueWait = registerOrExisting(reg, m.QueueWait)
	m.Skipped = registerOrExisting(reg, m.Skipped)
	for _, trigger := range ProfileCaptureTriggers {
		for _, result := range ProfileCaptureResults {
			m.Finished.WithLabelValues(trigger, result)
		}
	}
	return m
}

// CaptureFinished records one finished capture: its result, the time from
// claim to stored result, and how long it waited queued.
func (m *ProfileCaptureMetrics) CaptureFinished(trigger, result string, queued, ran time.Duration) {
	if m == nil {
		return
	}
	trigger = profileCaptureLabel(trigger, ProfileCaptureTriggers, "manual")
	m.Finished.WithLabelValues(trigger, profileCaptureLabel(result, ProfileCaptureResults, "unreachable")).Inc()
	m.Duration.WithLabelValues(trigger).Observe(ran.Seconds())
	m.QueueWait.WithLabelValues(trigger).Observe(queued.Seconds())
}

// AutomaticSkipped records an automatic capture schedd did not queue.
func (m *ProfileCaptureMetrics) AutomaticSkipped(trigger, reason string) {
	if m == nil {
		return
	}
	m.Skipped.WithLabelValues(profileCaptureLabel(trigger, ProfileCaptureTriggers, "manual"), profileCaptureLabel(reason, ProfileCaptureSkips, "unavailable")).Inc()
}

// profileCaptureLabel bounds a label value to its closed set.
func profileCaptureLabel(value string, allowed []string, fallback string) string {
	if containsString(allowed, value) {
		return value
	}
	return fallback
}
