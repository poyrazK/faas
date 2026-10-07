// adr: 638
package main

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/prometheus/client_golang/prometheus"
)

type durableEntityMetrics struct {
	registry       *prometheus.Registry
	operations     *prometheus.CounterVec
	uploadedBytes  *prometheus.CounterVec
	outcomes       *prometheus.CounterVec
	cleanup        *prometheus.CounterVec
	recoveries     *prometheus.CounterVec
	lastSweep      *prometheus.GaugeVec
	alarmDelay     *prometheus.HistogramVec
	inventory      *prometheus.CounterVec
	committedBytes *prometheus.HistogramVec
	currentBytes   *prometheus.HistogramVec
}

func newDurableEntityMetrics(registry *prometheus.Registry, prefix string) *durableEntityMetrics {
	m := &durableEntityMetrics{registry: registry}
	m.operations = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_storage_operations_total", Help: "Private entity storage calls by bounded operation and outcome; no entity identifiers.",
	}, []string{"operation", "outcome"}))
	m.uploadedBytes = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_uploaded_bytes_total", Help: "Encoded bytes in acknowledged uploads by object kind, including overwritten manifests. This measures upload volume, not retained or physical bucket bytes.",
	}, []string{"kind"}))
	m.outcomes = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_operations_total", Help: "Entity invocation, alarm delivery and maintenance outcomes; busy and conditional conflicts remain retryable.",
	}, []string{"operation", "outcome"}))
	m.cleanup = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_cleanup_objects_total", Help: "Cleanup candidates deleted, retained or failed. Deletion counts current keys, not versioned history or backups.",
	}, []string{"outcome"}))
	m.recoveries = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_maintenance_recoveries_total", Help: "Maintenance visits recovering an expired checkpoint owner.",
	}, []string{}))
	m.lastSweep = registerMetricsDiscoveryGaugeVec(registry, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_durable_entity_maintenance_last_sweep_timestamp_seconds", Help: "Last completed discovery rotation by this replica, including skipped/busy/failed entities. Zero until a rotation completes.",
	}, []string{}))
	m.alarmDelay = registerRealtimeRouteHistogramVec(registry, prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_durable_entity_alarm_delay_seconds", Help: "Delay from a saved deadline to an allowlisted delivery attempt; no delivery latency guarantee.",
		Buckets: prometheus.ExponentialBuckets(1, 4, 8),
	}, []string{}))
	m.inventory = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_inventory_samples_total", Help: "Bounded inventory visits by completion status; logical usage is proven through committed references, current-key bytes are observational.",
	}, []string{"outcome"}))
	m.committedBytes = registerRealtimeRouteHistogramVec(registry, prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_durable_entity_committed_bytes", Help: "Exact immutable bytes reachable from an entity snapshot in completed inventory samples. Repeated samples are not summed as storage inventory or billing.",
		Buckets: prometheus.ExponentialBuckets(1024, 16, 7),
	}, []string{}))
	m.currentBytes = registerRealtimeRouteHistogramVec(registry, prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_durable_entity_current_key_bytes", Help: "Provider-reported current-key bytes in completed per-entity scans; includes orphans/metadata, excludes version history and backups, and is not a transactional snapshot.",
		Buckets: prometheus.ExponentialBuckets(1024, 16, 7),
	}, []string{}))
	for _, operation := range []string{"get", "put", "list_entities", "list_objects", "delete"} {
		for _, outcome := range []string{"success", "missing", "conflict", "uncertain", "failed"} {
			m.operations.WithLabelValues(operation, outcome)
		}
	}
	for _, operation := range []string{"invoke", "alarm", "maintenance"} {
		for _, outcome := range []string{"success", "replay", "busy", "conflict", "stale", "uncertain", "invalid", "failed", "skipped", "limit", "pending"} {
			m.outcomes.WithLabelValues(operation, outcome)
		}
	}
	for _, kind := range []string{"manifest", "snapshot", "receipt", "maintenance", "probe", "other"} {
		m.uploadedBytes.WithLabelValues(kind)
	}
	for _, outcome := range []string{"deleted", "retained", "failed"} {
		m.cleanup.WithLabelValues(outcome)
	}
	m.recoveries.WithLabelValues()
	m.lastSweep.WithLabelValues()
	m.alarmDelay.WithLabelValues()
	for _, outcome := range []string{"partial", "complete", "current_bytes_unavailable"} {
		m.inventory.WithLabelValues(outcome)
	}
	m.committedBytes.WithLabelValues()
	m.currentBytes.WithLabelValues()
	return m
}

func durableEntityOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, durableentity.ErrBusy):
		return "busy"
	case errors.Is(err, durableentity.ErrConflict), errors.Is(err, durableentity.ErrRequestConflict):
		return "conflict"
	case errors.Is(err, durableentity.ErrStaleOwner):
		return "stale"
	case errors.Is(err, durableentity.ErrUncertain):
		return "uncertain"
	case errors.Is(err, durableentity.ErrInvalid), errors.Is(err, durableentity.ErrAlarmObsolete):
		return "invalid"
	case errors.Is(err, durableentity.ErrLimit):
		return "limit"
	case errors.Is(err, durableentity.ErrInventoryPending):
		return "pending"
	default:
		return "failed"
	}
}

func (m *durableEntityMetrics) observeResult(operation string, result durableentity.Result, err error) {
	if m == nil {
		return
	}
	outcome := durableEntityOutcome(err)
	if err == nil && result.Replayed {
		outcome = "replay"
	}
	m.outcomes.WithLabelValues(operation, outcome).Inc()
}

func (m *durableEntityMetrics) observeMaintenance(result durableentity.MaintenanceResult, err error) {
	if m == nil {
		return
	}
	outcome := durableEntityOutcome(err)
	if err == nil {
		switch {
		case result.Failed > 0 || result.Cleanup.Failed > 0:
			outcome = "failed"
		case result.Busy > 0:
			outcome = "busy"
		case result.Skipped > 0:
			outcome = "skipped"
		}
	}
	m.outcomes.WithLabelValues("maintenance", outcome).Inc()
	m.cleanup.WithLabelValues("deleted").Add(float64(result.Cleanup.Deleted))
	m.cleanup.WithLabelValues("retained").Add(float64(result.Cleanup.Retained))
	m.cleanup.WithLabelValues("failed").Add(float64(result.Cleanup.Failed))
	if result.Recovered {
		m.recoveries.WithLabelValues().Inc()
	}
	if err == nil && result.SweepCompleted {
		m.lastSweep.WithLabelValues().Set(float64(time.Now().Unix()))
	}
	if result.Inventory != nil {
		inventory := *result.Inventory
		outcome := "partial"
		if inventory.Complete {
			outcome = "complete"
			m.committedBytes.WithLabelValues().Observe(float64(inventory.Usage.TotalBytes()))
			if inventory.CurrentBytesKnown {
				m.currentBytes.WithLabelValues().Observe(float64(inventory.CurrentBytes))
			} else {
				outcome = "current_bytes_unavailable"
			}
		}
		m.inventory.WithLabelValues(outcome).Inc()
	}
}

func (m *durableEntityMetrics) observeAlarmDelay(at time.Time) {
	if m != nil {
		m.alarmDelay.WithLabelValues().Observe(max(0, time.Since(at).Seconds()))
	}
}
