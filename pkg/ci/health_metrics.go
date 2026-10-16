package ci

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

// HealthMetrics uses the daemon's private registry. All labels are closed enums;
// data read failures emit up=0 and omit dependent evidence rather than zero it.
type HealthMetrics struct {
	registry                                                                     *prometheus.Registry
	ctx                                                                          context.Context
	store                                                                        FleetHealthReader
	enabled                                                                      func() bool
	workerConfig                                                                 func() map[WorkerKind]bool
	configured, up, queue, oldest, used, limit, headroom, workers, workerEnabled *prometheus.Desc
}

func NewHealthMetrics(ctx context.Context, registry *prometheus.Registry, prefix string, store FleetHealthReader, enabled func() bool, workerConfig func() map[WorkerKind]bool) *HealthMetrics {
	m := &HealthMetrics{registry: registry, ctx: ctx, store: store, enabled: enabled, workerConfig: workerConfig,
		configured: prometheus.NewDesc(prefix+"_ci_health_enabled", "1 when CI operational observation is configured on this replica.", nil, nil),
		up:         prometheus.NewDesc(prefix+"_ci_health_up", "1 when durable CI health was read successfully during this scrape; omitted when observation is disabled.", nil, nil),
		queue:      prometheus.NewDesc(prefix+"_ci_queue_items", "CI queue items by overlapping bounded condition; due is time eligibility, not a safety/admission guarantee.", []string{"queue", "condition"}, nil),
		oldest:     prometheus.NewDesc(prefix+"_ci_queue_oldest_item_age_seconds", "Age since creation of the oldest retained pending or in-flight item, not duration in its current state.", []string{"queue"}, nil),
		used:       prometheus.NewDesc(prefix+"_ci_resource_used", "CI fleet resource usage; account-only limits are not fleet limits.", []string{"resource"}, nil),
		limit:      prometheus.NewDesc(prefix+"_ci_resource_limit", "Configured CI global resource limit, where one exists.", []string{"resource"}, nil),
		headroom:   prometheus.NewDesc(prefix+"_ci_resource_headroom", "Remaining CI global quota, clamped at zero.", []string{"resource"}, nil),
		workers:    prometheus.NewDesc(prefix+"_ci_worker_replicas", "Anonymous bounded CI worker observations by condition; retained crashed slots become stale until reused.", []string{"worker", "condition"}, nil)}
	m.workerEnabled = prometheus.NewDesc(prefix+"_ci_worker_enabled", "Boot configuration of workers owned by this daemon, independent of health reads and wired dependencies.", []string{"worker"}, nil)
	if err := registry.Register(m); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			return duplicate.ExistingCollector.(*HealthMetrics)
		}
		panic(err)
	}
	return m
}
func (m *HealthMetrics) Registry() *prometheus.Registry { return m.registry }
func (m *HealthMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{m.configured, m.up, m.queue, m.oldest, m.used, m.limit, m.headroom, m.workers, m.workerEnabled} {
		ch <- d
	}
}
func (m *HealthMetrics) Collect(ch chan<- prometheus.Metric) {
	if m.workerConfig != nil {
		configured := m.workerConfig()
		for _, kind := range workerKinds() {
			if enabled, owned := configured[kind]; owned {
				value := 0.0
				if enabled {
					value = 1
				}
				ch <- prometheus.MustNewConstMetric(m.workerEnabled, prometheus.GaugeValue, value, string(kind))
			}
		}
	}
	if m.enabled == nil || !m.enabled() {
		ch <- prometheus.MustNewConstMetric(m.configured, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(m.configured, prometheus.GaugeValue, 1)

	ctx, cancel := context.WithTimeout(m.ctx, api.CIHealthTimeoutSeconds*time.Second)
	defer cancel()
	var h Health
	err := ErrNotFound
	if m.store != nil {
		h, err = m.store.CIFleetHealth(ctx)
	}
	if err != nil {
		ch <- prometheus.MustNewConstMetric(m.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(m.up, prometheus.GaugeValue, 1)
	for _, q := range h.Queues {
		for _, c := range []struct {
			name  string
			value int64
		}{{"pending", q.Pending}, {"in_flight", q.InFlight}, {"dead", q.Dead}, {"due", q.Due}, {"stale_leases", q.StaleLeases}} {
			ch <- prometheus.MustNewConstMetric(m.queue, prometheus.GaugeValue, float64(c.value), q.Name, c.name)
		}
		ch <- prometheus.MustNewConstMetric(m.oldest, prometheus.GaugeValue, float64(q.OldestItemAgeSeconds), q.Name)
	}
	for _, q := range h.Quotas {
		ch <- prometheus.MustNewConstMetric(m.used, prometheus.GaugeValue, float64(q.Used), q.Resource)
		if q.Limit != nil {
			ch <- prometheus.MustNewConstMetric(m.limit, prometheus.GaugeValue, float64(*q.Limit), q.Resource)
		}
		if q.Headroom != nil {
			ch <- prometheus.MustNewConstMetric(m.headroom, prometheus.GaugeValue, float64(*q.Headroom), q.Resource)
		}
	}
	for _, w := range h.Workers {
		for _, c := range workerConditions() {
			ch <- prometheus.MustNewConstMetric(m.workers, prometheus.GaugeValue, float64(w.Replicas[c]), string(w.Name), c)
		}
	}
}
