package main

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// A sample describes the shared PostgreSQL relations. It is repeated by apid
// replicas, so dashboards use max rather than sum across instances.
type managedRealtimeHistoryMetrics struct {
	registry       *prometheus.Registry
	relationBytes  *prometheus.GaugeVec
	sampleSuccess  prometheus.Gauge
	lastSample     prometheus.Gauge
	prunedMessages prometheus.Counter
	pruneFailures  prometheus.Counter
}

func newManagedRealtimeHistoryMetrics(registry *prometheus.Registry, prefix string) *managedRealtimeHistoryMetrics {
	m := &managedRealtimeHistoryMetrics{
		registry: registry,
		relationBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_realtime_history_relation_bytes",
			Help: "Physical PostgreSQL bytes allocated to managed realtime history tables and their indexes, sampled after the reaper pass.",
		}, []string{"relation"}),
		sampleSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_realtime_history_sample_success",
			Help: "Whether the latest managed realtime history storage sample succeeded.",
		}),
		lastSample: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_realtime_history_last_sample_timestamp_seconds",
			Help: "Unix timestamp of the latest successful managed realtime history storage sample.",
		}),
		prunedMessages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_realtime_history_pruned_messages_total",
			Help: "Expired managed realtime history messages physically removed by this apid replica.",
		}),
		pruneFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_realtime_history_prune_failures_total",
			Help: "Failed managed realtime history reaper passes on this apid replica.",
		}),
	}
	for _, relation := range []string{"heads", "messages"} {
		m.relationBytes.WithLabelValues(relation)
	}
	if err := registry.Register(m.relationBytes); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.relationBytes = duplicate.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := registry.Register(m.sampleSuccess); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.sampleSuccess = duplicate.ExistingCollector.(prometheus.Gauge)
		} else {
			panic(err)
		}
	}
	if err := registry.Register(m.lastSample); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.lastSample = duplicate.ExistingCollector.(prometheus.Gauge)
		} else {
			panic(err)
		}
	}
	if err := registry.Register(m.prunedMessages); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.prunedMessages = duplicate.ExistingCollector.(prometheus.Counter)
		} else {
			panic(err)
		}
	}
	if err := registry.Register(m.pruneFailures); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.pruneFailures = duplicate.ExistingCollector.(prometheus.Counter)
		} else {
			panic(err)
		}
	}
	return m
}

func (m *managedRealtimeHistoryMetrics) observePrune(removed int64, err error) {
	if m == nil {
		return
	}
	if err != nil {
		m.pruneFailures.Inc()
		return
	}
	if removed > 0 {
		m.prunedMessages.Add(float64(removed))
	}
}

func (m *managedRealtimeHistoryMetrics) observeStorage(stats state.ManagedRealtimeHistoryStorageStats, at time.Time) {
	if m == nil {
		return
	}
	m.relationBytes.WithLabelValues("heads").Set(float64(stats.HeadsRelationBytes))
	m.relationBytes.WithLabelValues("messages").Set(float64(stats.MessagesRelationBytes))
	m.lastSample.Set(float64(at.Unix()))
	m.sampleSuccess.Set(1)
}

func (m *managedRealtimeHistoryMetrics) observeStorageFailure() {
	if m != nil {
		m.sampleSuccess.Set(0)
	}
}
