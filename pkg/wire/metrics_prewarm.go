package wire

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PrewarmMetrics is the lifecycle metric surface for scheduled and predicted
// demand-window restores. The event label is deliberately a closed vocabulary
// so dashboards cannot accidentally create one series per intent or error.
type PrewarmMetrics struct {
	IntentEventsTotal      *prometheus.CounterVec
	AdmittedInstancesTotal prometheus.Counter
	FireOffsetSeconds      prometheus.Histogram
	IntentAgeSeconds       prometheus.Histogram
}

const (
	prewarmEventScheduled = "scheduled"
	prewarmEventSucceeded = "succeeded"
	prewarmEventPartial   = "partial"
	prewarmEventFailed    = "failed"
	prewarmEventExpired   = "expired"
	prewarmEventCancelled = "cancelled"
)

// NewPrewarmMetrics creates and registers the prewarm metric families with a
// daemon's registry. A nil registerer is supported for unit-test/degraded
// construction paths, matching the other wire metric bundles.
func NewPrewarmMetrics(reg prometheus.Registerer) *PrewarmMetrics {
	m := &PrewarmMetrics{
		IntentEventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "prewarm_intents_total",
				Help: "Total prewarm intent lifecycle events by event type.",
			},
			[]string{"event"},
		),
		AdmittedInstancesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prewarm_admitted_instances_total",
			Help: "Total instances admitted by scheduled prewarm restores.",
		}),
		FireOffsetSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "prewarm_fire_offset_seconds",
			Help:    "Seconds between a prewarm fire and wake_at; negative means the restore fired ahead of demand.",
			Buckets: []float64{-300, -120, -60, -30, -10, 0, 10, 30, 60, 120, 300, 900},
		}),
		IntentAgeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "prewarm_intent_age_seconds",
			Help:    "Age of a prewarm intent when it reaches a terminal fired outcome.",
			Buckets: []float64{1, 5, 10, 30, 60, 300, 900, 3600, 21600, 86400},
		}),
	}

	if reg != nil {
		reg.MustRegister(m.IntentEventsTotal, m.AdmittedInstancesTotal, m.FireOffsetSeconds, m.IntentAgeSeconds)
		for _, event := range []string{
			prewarmEventScheduled,
			prewarmEventSucceeded,
			prewarmEventPartial,
			prewarmEventFailed,
			prewarmEventExpired,
			prewarmEventCancelled,
		} {
			// Pre-instantiate the closed set so a newly booted daemon exposes
			// stable zero-valued series before the first customer event.
			m.IntentEventsTotal.WithLabelValues(event)
		}
	}
	return m
}

func (m *PrewarmMetrics) ObserveScheduled() {
	if m == nil {
		return
	}
	m.IntentEventsTotal.WithLabelValues(prewarmEventScheduled).Inc()
}

func (m *PrewarmMetrics) ObserveCancelled() {
	if m == nil {
		return
	}
	m.IntentEventsTotal.WithLabelValues(prewarmEventCancelled).Inc()
}

func (m *PrewarmMetrics) ObserveExpired() {
	if m == nil {
		return
	}
	m.IntentEventsTotal.WithLabelValues(prewarmEventExpired).Inc()
}

// ObserveFired records the scheduler's terminal outcome without exposing
// app, account, intent, or backend error labels. Negative fire offsets mean
// the scheduler restored capacity before wake_at; positive values indicate a
// late fire.
func (m *PrewarmMetrics) ObserveFired(outcome string, admitted int, wakeAt, createdAt, firedAt time.Time) {
	if m == nil {
		return
	}
	event := prewarmEventFailed
	switch outcome {
	case prewarmEventSucceeded, prewarmEventPartial:
		event = outcome
	}
	m.IntentEventsTotal.WithLabelValues(event).Inc()
	if admitted > 0 {
		m.AdmittedInstancesTotal.Add(float64(admitted))
	}
	if !wakeAt.IsZero() && !firedAt.IsZero() {
		m.FireOffsetSeconds.Observe(firedAt.Sub(wakeAt).Seconds())
	}
	if !createdAt.IsZero() && !firedAt.IsZero() {
		age := firedAt.Sub(createdAt).Seconds()
		if age >= 0 {
			m.IntentAgeSeconds.Observe(age)
		}
	}
}
