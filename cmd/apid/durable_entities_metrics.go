// adr: 712
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/prometheus/client_golang/prometheus"
)

type durableEntityMetrics struct {
	registry        *prometheus.Registry
	operations      *prometheus.CounterVec
	storageDuration *prometheus.HistogramVec
	duration        *prometheus.HistogramVec
	uploadedBytes   *prometheus.CounterVec
	outcomes        *prometheus.CounterVec
	cleanup         *prometheus.CounterVec
	recoveries      *prometheus.CounterVec
	lastSweep       *prometheus.GaugeVec
	alarmDelay      *prometheus.HistogramVec
	inventory       *prometheus.CounterVec
	committedBytes  *prometheus.HistogramVec
	currentBytes    *prometheus.HistogramVec
	outboxPending   *prometheus.HistogramVec
	health          *durableEntityHealthMetrics
}

func newDurableEntityMetrics(registry *prometheus.Registry, prefix string) *durableEntityMetrics {
	m := &durableEntityMetrics{registry: registry}
	m.operations = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_storage_operations_total", Help: "Private entity storage calls by bounded operation and outcome; no entity identifiers.",
	}, []string{"operation", "outcome"}))
	m.storageDuration = registerRealtimeRouteHistogramVec(registry, prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_durable_entity_storage_duration_seconds", Help: "Elapsed private storage calls including failures; bounded operation labels, no object keys.", Buckets: append(append([]float64{}, prometheus.DefBuckets...), 25, 30, 60),
	}, []string{"operation"}))
	m.duration = registerRealtimeRouteHistogramVec(registry, prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_durable_entity_operation_duration_seconds", Help: "Elapsed admitted engine operations including queue, ownership and cleanup where applicable. Samples overlap; not HTTP latency, an SLO or billing.", Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 30, 60},
	}, []string{"operation"}))
	m.uploadedBytes = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_uploaded_bytes_total", Help: "Encoded bytes in acknowledged uploads by object kind, including overwritten manifests. This measures upload volume, not retained or physical bucket bytes.",
	}, []string{"kind"}))
	m.outcomes = registerRealtimeRouteCounterVec(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_durable_entity_operations_total", Help: "Engine operation and validator verdict outcomes; excludes HTTP admission failures. Busy and conditional conflicts remain retryable.",
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
	m.outboxPending = registerRealtimeRouteHistogramVec(registry, prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_durable_entity_outbox_pending_messages", Help: "Pending FIFO queue size in exact-entity inspection samples; repeated samples are not summed as a fleet backlog.",
		Buckets: []float64{0, 1, 8, 16, 64, 128},
	}, []string{}))
	for _, operation := range []string{"get", "put", "list_entities", "list_objects", "delete"} {
		for _, outcome := range []string{"success", "missing", "conflict", "uncertain", "failed"} {
			m.operations.WithLabelValues(operation, outcome)
		}
	}
	m.health = newDurableEntityHealthMetrics(registry, prefix)
	for _, operation := range []string{"invoke", "alarm", "maintenance", "outbox", "retry_alarm", "retry_outbox", "restore", "validate_restore", "export"} {
		for _, outcome := range []string{"success", "replay", "busy", "conflict", "stale", "uncertain", "invalid", "failed", "skipped", "limit", "pending", "backoff", "exhausted", "rejected", "cancelled", "timeout", "corrupt", "missing"} {
			m.outcomes.WithLabelValues(operation, outcome)
		}
	}
	for _, kind := range []string{"manifest", "snapshot", "receipt", "maintenance", "probe", "alarm_index", "outbox_index", "other"} {
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
	m.outboxPending.WithLabelValues()
	return m
}

func durableEntityOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, durableentity.ErrCorrupt):
		return "corrupt"
	case errors.Is(err, durableentity.ErrUncertain):
		return "uncertain"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, durableentity.ErrRestoreRejected):
		return "rejected"
	case errors.Is(err, durableentity.ErrNotFound):
		return "missing"
	case errors.Is(err, durableentity.ErrBusy):
		return "busy"
	case errors.Is(err, durableentity.ErrConflict), errors.Is(err, durableentity.ErrRequestConflict), errors.Is(err, durableentity.ErrRecoveryObsolete), errors.Is(err, durableentity.ErrRestoreObsolete):
		return "conflict"
	case errors.Is(err, durableentity.ErrStaleOwner):
		return "stale"
	case errors.Is(err, durableentity.ErrInvalid), errors.Is(err, durableentity.ErrAlarmObsolete), errors.Is(err, durableentity.ErrOutboxObsolete):
		return "invalid"
	case errors.Is(err, durableentity.ErrLimit):
		return "limit"
	case errors.Is(err, durableentity.ErrInventoryPending):
		return "pending"
	case errors.Is(err, durableentity.ErrAlarmBackoff), errors.Is(err, durableentity.ErrOutboxBackoff):
		return "backoff"
	case errors.Is(err, durableentity.ErrAlarmExhausted), errors.Is(err, durableentity.ErrOutboxExhausted):
		return "exhausted"
	default:
		var problem *api.Problem
		if errors.As(err, &problem) {
			switch problem.Status {
			case http.StatusConflict:
				return "conflict"
			case http.StatusUnprocessableEntity, http.StatusBadRequest:
				return "invalid"
			}
		}
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

// observeDuration records only fixed, internal operation names. Identity must
// never become a metric label, even when a caller passes an unexpected value.
func (m *durableEntityMetrics) observeDuration(operation string, started time.Time) {
	if m == nil {
		return
	}
	switch operation {
	case "invoke", "alarm", "outbox", "maintenance", "retry_alarm", "retry_outbox", "restore", "validate_restore", "export":
		m.duration.WithLabelValues(operation).Observe(max(0, time.Since(started).Seconds()))
	}
}
