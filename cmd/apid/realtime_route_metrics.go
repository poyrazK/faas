package main

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	managedRealtimeRoutePublishDecisions = []string{
		"routing_disabled",
		"route_store_unavailable",
		"directory_error",
		"overflow",
		"unready_fallback",
		"targeted",
		"no_subscribers",
	}
	managedRealtimeRouteRebuildOutcomes  = []string{"started", "idle", "error", "canceled"}
	managedRealtimeRoutePassOutcomes     = []string{"complete", "incomplete", "error", "canceled"}
	managedRealtimeRouteSnapshotOutcomes = []string{"success", "error", "canceled"}
)

// managedRealtimeChannelRouteMetrics reports routing decisions and repair
// progress with closed labels. Endpoint, channel, and node identifiers are
// intentionally absent so traffic volume cannot create unbounded series.
type managedRealtimeChannelRouteMetrics struct {
	publishDecisions  *prometheus.CounterVec
	publishRecipients *prometheus.HistogramVec
	rebuildChecks     *prometheus.CounterVec
	reconcilePasses   *prometheus.CounterVec
	reconcileDuration prometheus.Histogram
	nodeSnapshots     *prometheus.CounterVec
}

func newManagedRealtimeChannelRouteMetrics(registry *prometheus.Registry, prefix string) *managedRealtimeChannelRouteMetrics {
	if registry == nil || prefix == "" {
		return nil
	}
	metrics := &managedRealtimeChannelRouteMetrics{
		publishDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_realtime_channel_route_publish_decisions_total",
			Help: "Realtime channel publish routing decisions by bounded fallback reason.",
		}, []string{"decision"}),
		publishRecipients: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prefix + "_realtime_channel_route_publish_recipients",
			Help:    "Active compute nodes selected for each realtime channel publish by routing decision.",
			Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024},
		}, []string{"decision"}),
		rebuildChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_realtime_channel_route_rebuild_checks_total",
			Help: "Realtime channel route overflow rebuild checks by outcome.",
		}, []string{"outcome"}),
		reconcilePasses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_realtime_channel_route_reconcile_passes_total",
			Help: "Realtime channel route reconciliation passes by outcome.",
		}, []string{"outcome"}),
		reconcileDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    prefix + "_realtime_channel_route_reconcile_duration_seconds",
			Help:    "Duration of a realtime channel route reconciliation pass.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}),
		nodeSnapshots: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_realtime_channel_route_node_snapshots_total",
			Help: "Realtime channel route node snapshot attempts by outcome.",
		}, []string{"outcome"}),
	}

	metrics.publishDecisions = registerRealtimeRouteCounterVec(registry, metrics.publishDecisions)
	metrics.publishRecipients = registerRealtimeRouteHistogramVec(registry, metrics.publishRecipients)
	metrics.rebuildChecks = registerRealtimeRouteCounterVec(registry, metrics.rebuildChecks)
	metrics.reconcilePasses = registerRealtimeRouteCounterVec(registry, metrics.reconcilePasses)
	metrics.reconcileDuration = registerRealtimeRouteHistogram(registry, metrics.reconcileDuration)
	metrics.nodeSnapshots = registerRealtimeRouteCounterVec(registry, metrics.nodeSnapshots)
	for _, decision := range managedRealtimeRoutePublishDecisions {
		metrics.publishDecisions.WithLabelValues(decision)
		metrics.publishRecipients.WithLabelValues(decision)
	}
	for _, outcome := range managedRealtimeRouteRebuildOutcomes {
		metrics.rebuildChecks.WithLabelValues(outcome)
	}
	for _, outcome := range managedRealtimeRoutePassOutcomes {
		metrics.reconcilePasses.WithLabelValues(outcome)
	}
	for _, outcome := range managedRealtimeRouteSnapshotOutcomes {
		metrics.nodeSnapshots.WithLabelValues(outcome)
	}
	return metrics
}

func registerRealtimeRouteCounterVec(registry *prometheus.Registry, candidate *prometheus.CounterVec) *prometheus.CounterVec {
	if err := registry.Register(candidate); err == nil {
		return candidate
	} else {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
		panic(err)
	}
}

func registerRealtimeRouteHistogramVec(registry *prometheus.Registry, candidate *prometheus.HistogramVec) *prometheus.HistogramVec {
	if err := registry.Register(candidate); err == nil {
		return candidate
	} else {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.HistogramVec); ok {
				return existing
			}
		}
		panic(err)
	}
}

func registerRealtimeRouteHistogram(registry *prometheus.Registry, candidate prometheus.Histogram) prometheus.Histogram {
	if err := registry.Register(candidate); err == nil {
		return candidate
	} else {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(prometheus.Histogram); ok {
				return existing
			}
		}
		panic(err)
	}
}

func (m *managedRealtimeChannelRouteMetrics) publish(decision string, recipients int) {
	if m == nil {
		return
	}
	m.publishDecisions.WithLabelValues(decision).Inc()
	m.publishRecipients.WithLabelValues(decision).Observe(float64(recipients))
}

func (m *managedRealtimeChannelRouteMetrics) rebuildCheck(outcome string) {
	if m != nil {
		m.rebuildChecks.WithLabelValues(outcome).Inc()
	}
}

func (m *managedRealtimeChannelRouteMetrics) reconcilePass(outcome string, durationSeconds float64) {
	if m == nil {
		return
	}
	m.reconcilePasses.WithLabelValues(outcome).Inc()
	m.reconcileDuration.Observe(durationSeconds)
}

func (m *managedRealtimeChannelRouteMetrics) nodeSnapshot(outcome string) {
	if m != nil {
		m.nodeSnapshots.WithLabelValues(outcome).Inc()
	}
}
