// adr: 937
package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type durableEntityHealthMetrics struct {
	enabled, pollSuccess, lastRotation, entities *prometheus.GaugeVec
	pending, exhausted, oldest, unknownAge       *prometheus.GaugeVec
	scans                                        *prometheus.CounterVec
}

func newDurableEntityHealthMetrics(registry *prometheus.Registry, prefix string) *durableEntityHealthMetrics {
	gauge := func(name, help string, labels ...string) *prometheus.GaugeVec {
		return registerMetricsDiscoveryGaugeVec(registry, prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_durable_entity_health_" + name, Help: help}, labels))
	}
	m := &durableEntityHealthMetrics{
		enabled:      gauge("enabled", "Whether this replica runs the opt-in read-only health scanner."),
		pollSuccess:  gauge("poll_success", "Whether health discovery is healthy; failed/partial rotations retain previous observations."),
		lastRotation: gauge("last_success_timestamp_seconds", "Completion time of the last rotation with no listing or entity-read failures; zero until one completes."),
		entities:     gauge("observed_entities", "Allowlisted entity observations in the last successful rotation, not an atomic inventory."),
		pending:      gauge("pending_work", "Alarm or message observations from the last successful rotation, not exact fleet counts.", "target"),
		exhausted:    gauge("exhausted_entities", "Entities with exhausted selected work observed in the last successful rotation.", "target"),
		oldest:       gauge("oldest_pending_timestamp_seconds", "Oldest observed saved alarm deadline or known outbox timestamp from the last successful rotation; zero when none known.", "target"),
		unknownAge:   gauge("unknown_age_messages", "Pending outbox messages without a stored timestamp in the last successful rotation."),
		scans:        registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_durable_entity_health_pages_total", Help: "Health pages by bounded success, partial or failed result; no entity labels."}, []string{"outcome"})),
	}
	for _, g := range []*prometheus.GaugeVec{m.enabled, m.pollSuccess, m.lastRotation, m.entities, m.unknownAge} {
		g.WithLabelValues()
	}
	for _, target := range []string{"alarm", "outbox"} {
		for _, g := range []*prometheus.GaugeVec{m.pending, m.exhausted, m.oldest} {
			g.WithLabelValues(target)
		}
	}
	for _, outcome := range []string{"success", "partial", "failed"} {
		m.scans.WithLabelValues(outcome)
	}
	return m
}

func healthTimestamp(at *time.Time) float64 {
	if at == nil {
		return 0
	}
	return float64(at.Unix())
}

func (m *durableEntityHealthMetrics) publish(r durableEntityHealthRotation, now time.Time) {
	m.entities.WithLabelValues().Set(float64(r.entities))
	m.pending.WithLabelValues("alarm").Set(float64(r.alarms))
	m.pending.WithLabelValues("outbox").Set(float64(r.outbox))
	m.exhausted.WithLabelValues("alarm").Set(float64(r.alarmExhausted))
	m.exhausted.WithLabelValues("outbox").Set(float64(r.outboxExhausted))
	m.oldest.WithLabelValues("alarm").Set(healthTimestamp(r.oldestAlarm))
	m.oldest.WithLabelValues("outbox").Set(healthTimestamp(r.oldestOutbox))
	m.unknownAge.WithLabelValues().Set(float64(r.unknownAge))
	m.lastRotation.WithLabelValues().Set(float64(now.Unix()))
}
