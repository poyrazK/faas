package hosting

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics has no site, account, hostname, provider error, or release labels.
type Metrics struct {
	registry                                            prometheus.Registerer
	attempts                                            *prometheus.CounterVec
	duration                                            *prometheus.HistogramVec
	passes                                              *prometheus.CounterVec
	pending, oldest, observedAt, available, lastSuccess prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer, prefix string) *Metrics {
	if reg == nil {
		return nil
	}
	m := &Metrics{registry: reg,
		attempts:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_hosting_route_publication_attempts_total", Help: "Durable routing head publication or verification attempts by bounded outcome."}, []string{"outcome"}),
		duration:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: prefix + "_hosting_route_publication_duration_seconds", Help: "Durable routing publication/verification and acknowledgement duration.", Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60}}, []string{"outcome"}),
		passes:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_hosting_reconcile_passes_total", Help: "Hosting reconciliation passes by bounded outcome."}, []string{"outcome"}),
		pending:     prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_hosting_route_pending", Help: "Durable sites whose latest intent is not acknowledged by the provider."}),
		oldest:      prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_hosting_route_oldest_pending_seconds", Help: "Age of the oldest continuously pending durable route publication at the last observation."}),
		observedAt:  prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_hosting_route_backlog_observed_unixtime", Help: "Unix time of the latest successful durable publication backlog observation."}),
		available:   prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_hosting_route_backlog_available", Help: "Whether the latest durable publication backlog read succeeded."}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_hosting_reconcile_last_success_unixtime", Help: "Unix time of the latest successful hosting reconciliation pass."}),
	}
	register := func(c prometheus.Collector) prometheus.Collector {
		if err := reg.Register(c); err != nil {
			var existing prometheus.AlreadyRegisteredError
			if errors.As(err, &existing) {
				return existing.ExistingCollector
			}
			panic(err)
		}
		return c
	}
	m.attempts = register(m.attempts).(*prometheus.CounterVec)
	m.duration = register(m.duration).(*prometheus.HistogramVec)
	m.passes = register(m.passes).(*prometheus.CounterVec)
	m.pending = register(m.pending).(prometheus.Gauge)
	m.oldest = register(m.oldest).(prometheus.Gauge)
	m.observedAt = register(m.observedAt).(prometheus.Gauge)
	m.available = register(m.available).(prometheus.Gauge)
	m.lastSuccess = register(m.lastSuccess).(prometheus.Gauge)
	for _, outcome := range []string{"published", "receipt_unavailable", api.HostingRouteErrorOrigin, api.HostingRouteErrorProvider, api.HostingRouteErrorStale} {
		m.attempts.WithLabelValues(outcome)
	}
	for _, outcome := range []string{"success", "failure", "canceled"} {
		m.passes.WithLabelValues(outcome)
	}
	return m
}

func (m *Metrics) Registry() prometheus.Registerer {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *Metrics) observePublication(outcome string, elapsed time.Duration) {
	if m == nil {
		return
	}
	switch outcome {
	case "published", "receipt_unavailable", api.HostingRouteErrorOrigin, api.HostingRouteErrorProvider, api.HostingRouteErrorStale:
	default:
		outcome = api.HostingRouteErrorProvider
	}
	m.attempts.WithLabelValues(outcome).Inc()
	m.duration.WithLabelValues(outcome).Observe(elapsed.Seconds())
}

func (m *Metrics) observePass(err error, canceled bool, at time.Time) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "failure"
		if canceled {
			outcome = "canceled"
		}
	} else {
		m.lastSuccess.Set(float64(at.Unix()))
	}
	m.passes.WithLabelValues(outcome).Inc()
}

func (m *Metrics) observeBacklog(backlog state.HostingRouteBacklog, err error, at time.Time) {
	if err != nil {
		m.available.Set(0)
		return
	}
	m.available.Set(1)
	m.pending.Set(float64(backlog.Pending))
	age := 0.0
	if !backlog.OldestPendingAt.IsZero() {
		age = max(0, at.Sub(backlog.OldestPendingAt).Seconds())
	}
	m.oldest.Set(age)
	m.observedAt.Set(float64(at.Unix()))
}
