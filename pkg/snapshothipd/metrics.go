// Package snapshothipd contains the node-local snapshot prepositioning
// worker. The name intentionally mirrors the operator-facing daemon/metric
// vocabulary from issue #1054, even though the first implementation runs as
// a vmmd-owned worker rather than a separate systemd process.
package snapshothipd

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the small observation seam used by Runner. Keeping it separate
// lets the worker remain testable without binding tests to a Prometheus
// registry.
type Metrics interface {
	ObserveFanout(outcome, region string)
}

// PrometheusMetrics exposes the issue #1054 acceptance metric on the daemon's
// existing registry. The region label is intentionally the node's configured
// locality, never an IP address or an arbitrary storage key.
type PrometheusMetrics struct {
	fanoutTotal   *prometheus.CounterVec
	fanoutLatency *prometheus.HistogramVec
}

// NewPrometheusMetrics registers the exact operator-facing metric name on an
// existing daemon registry. A separate registry is not used because vmmd's
// /metrics endpoint is the established operational surface.
//
// regions is optional because the node's authoritative region normally comes
// from compute_nodes and is attached to each claimed job. When supplied, the
// configured regions are pre-instantiated; with no configured region, an
// empty-region pair is still created so both closed outcomes are visible from
// process start. The worker adds the authoritative region label when it first
// observes a job.
func NewPrometheusMetrics(reg *prometheus.Registry, regions ...string) (*PrometheusMetrics, error) {
	if reg == nil {
		return nil, errors.New("snapshothipd: nil prometheus registry")
	}
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "snapshothipd_fanout_total",
		Help: "Snapshot fan-out attempts by terminal outcome and compute-node region (issue #1054).",
	}, []string{"outcome", "region"})
	if err := reg.Register(c); err != nil {
		return nil, err
	}
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "snapshothipd_fanout_latency_seconds",
		Help:    "Snapshot fan-out queue-to-ready latency by compute-node region (issue #1054).",
		Buckets: []float64{.01, .025, .05, .1, .2, .5, 1, 2, 5, 10, 30, 60},
	}, []string{"region"})
	if err := reg.Register(h); err != nil {
		return nil, err
	}
	if len(regions) == 0 {
		regions = []string{""}
	}
	seen := make(map[string]struct{}, len(regions))
	for _, region := range regions {
		if _, ok := seen[region]; ok {
			continue
		}
		seen[region] = struct{}{}
		for _, outcome := range []string{"ready", "failed"} {
			c.WithLabelValues(outcome, region)
		}
		h.WithLabelValues(region)
	}
	return &PrometheusMetrics{fanoutTotal: c, fanoutLatency: h}, nil
}

// ObserveFanoutLatency records the durable queue-to-ready latency used by the
// M9 prepositioned-wake acceptance gate.
func (m *PrometheusMetrics) ObserveFanoutLatency(region string, latency time.Duration) {
	if m == nil || m.fanoutLatency == nil || latency < 0 {
		return
	}
	m.fanoutLatency.WithLabelValues(region).Observe(latency.Seconds())
}

func (m *PrometheusMetrics) ObserveFanout(outcome, region string) {
	if m == nil || m.fanoutTotal == nil {
		return
	}
	m.fanoutTotal.WithLabelValues(outcome, region).Inc()
}
