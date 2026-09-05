package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var metricsDiscoveryJobs = []string{
	"gatewayd-internal",
	"promtail-compute",
}

var metricsDiscoveryOutcomes = []string{
	"success",
	"forbidden",
	"unavailable",
	"error",
}

// metricsDiscoveryMetrics records the health of the producer side of the
// Prometheus HTTP-SD contract. The target count is intentionally a gauge: it
// describes the last successful registry snapshot, while last_success keeps
// a failed refresh from looking healthy merely because the /metrics scrape is
// still succeeding.
type metricsDiscoveryMetrics struct {
	registry *prometheus.Registry

	requests       *prometheus.CounterVec
	registryNodes  *prometheus.GaugeVec
	targets        *prometheus.GaugeVec
	invalidTargets *prometheus.GaugeVec
	lastSuccess    *prometheus.GaugeVec
}

func newMetricsDiscoveryMetrics(registry *prometheus.Registry, prefix string) *metricsDiscoveryMetrics {
	m := &metricsDiscoveryMetrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_metrics_discovery_requests_total",
			Help: "HTTP-SD discovery requests by job and outcome. Outcomes are success, forbidden, unavailable, and error.",
		}, []string{"job", "outcome"}),
		registryNodes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_metrics_discovery_registry_nodes",
			Help: "Active compute registry rows with a configured gateway endpoint in the last successful HTTP-SD snapshot.",
		}, []string{"job"}),
		targets: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_metrics_discovery_targets",
			Help: "Prometheus scrape targets returned by the last successful HTTP-SD snapshot.",
		}, []string{"job"}),
		invalidTargets: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_metrics_discovery_invalid_targets",
			Help: "Active compute registry rows omitted from the last successful HTTP-SD snapshot because their target or stable name was invalid.",
		}, []string{"job"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_metrics_discovery_last_success_timestamp_seconds",
			Help: "Unix timestamp of the most recent successful compute metrics HTTP-SD registry snapshot.",
		}, []string{"job"}),
	}

	registry.MustRegister(
		m.requests,
		m.registryNodes,
		m.targets,
		m.invalidTargets,
		m.lastSuccess,
	)
	for _, job := range metricsDiscoveryJobs {
		for _, outcome := range metricsDiscoveryOutcomes {
			m.requests.WithLabelValues(job, outcome)
		}
		m.registryNodes.WithLabelValues(job)
		m.targets.WithLabelValues(job)
		m.invalidTargets.WithLabelValues(job)
		m.lastSuccess.WithLabelValues(job)
	}
	return m
}

func (m *metricsDiscoveryMetrics) request(job, outcome string) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(job, outcome).Inc()
}

func (m *metricsDiscoveryMetrics) snapshot(job string, registryNodes, targets, invalidTargets int) {
	if m == nil {
		return
	}
	m.registryNodes.WithLabelValues(job).Set(float64(registryNodes))
	m.targets.WithLabelValues(job).Set(float64(targets))
	m.invalidTargets.WithLabelValues(job).Set(float64(invalidTargets))
	m.lastSuccess.WithLabelValues(job).Set(float64(time.Now().Unix()))
}
