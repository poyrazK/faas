package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type domainVerificationMetrics struct {
	cycles                              *prometheus.CounterVec
	batch, backlog, oldest, lastSuccess prometheus.Gauge
	results                             *prometheus.CounterVec
	publications                        *prometheus.CounterVec
}

func newDomainVerificationMetrics(reg prometheus.Registerer, prefix string) *domainVerificationMetrics {
	m := &domainVerificationMetrics{
		cycles:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_domain_verification_cycles_total", Help: "Domain verification cycles by outcome."}, []string{"outcome"}),
		batch:        prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_domain_verification_batch_size", Help: "Domains claimed by the latest verification cycle."}),
		backlog:      prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_domain_verification_backlog", Help: "Pending, non-expired custom domains."}),
		oldest:       prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_domain_verification_oldest_due_seconds", Help: "Age of the oldest due custom-domain verification."}),
		lastSuccess:  prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_domain_verification_last_success_unixtime", Help: "Unix time of the latest completed domain-verification cycle."}),
		results:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_domain_verification_results_total", Help: "Custom-domain verification probe outcomes."}, []string{"result"}),
		publications: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_domain_verification_publications_total", Help: "Custom-domain verification publication outcomes."}, []string{"outcome"}),
	}
	register := func(c prometheus.Collector) prometheus.Collector {
		if err := reg.Register(c); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				return already.ExistingCollector
			}
			panic(err)
		}
		return c
	}
	m.cycles = register(m.cycles).(*prometheus.CounterVec)
	m.batch = register(m.batch).(prometheus.Gauge)
	m.backlog = register(m.backlog).(prometheus.Gauge)
	m.oldest = register(m.oldest).(prometheus.Gauge)
	m.lastSuccess = register(m.lastSuccess).(prometheus.Gauge)
	m.results = register(m.results).(*prometheus.CounterVec)
	m.publications = register(m.publications).(*prometheus.CounterVec)
	for _, outcome := range []string{"success", "stale", "refused", "error"} {
		m.publications.WithLabelValues(outcome)
	}
	for _, v := range []string{"success", "failure", "error"} {
		m.results.WithLabelValues(v)
	}
	for _, v := range []string{"success", "error"} {
		m.cycles.WithLabelValues(v)
	}
	m.batch.Set(0)
	m.backlog.Set(0)
	m.oldest.Set(0)
	m.lastSuccess.Set(0)
	return m
}

func (m *domainVerificationMetrics) recordPublication(matched bool, err error) {
	if m == nil || m.publications == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		var aggregate *state.TrafficPolicyAggregateError
		var analysis *state.TrafficPolicyAnalysisError
		if errors.As(err, &aggregate) || errors.As(err, &analysis) {
			outcome = "refused"
		}
	} else if !matched {
		outcome = "stale"
	}
	m.publications.WithLabelValues(outcome).Inc()
}
