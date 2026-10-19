package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type PacingStatistics struct {
	CoolingDomains    int64
	WaitingRecipients int64
}

func (s *PGStore) PacingStatistics(ctx context.Context) (PacingStatistics, error) {
	r, err := s.queries.PacingStatistics(ctx, s.pool, s.namespace)
	return PacingStatistics{CoolingDomains: r.CoolingDomains, WaitingRecipients: r.WaitingRecipients}, err
}

type PacingStatisticsReader interface {
	PacingStatistics(context.Context) (PacingStatistics, error)
}

type PacingMonitor struct {
	source  PacingStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewPacingMonitor(source PacingStatisticsReader, enabled func() bool) *PacingMonitor {
	m := &PacingMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal recipient domain pacing gate."},
		{"stats_up", "Whether durable domain pacing aggregates were read successfully."},
		{"cooling_domains", "Recipient domains with a reserved submission cooldown."},
		{"waiting_recipients", "Due queued recipients waiting behind a recipient domain cooldown."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_domain_pacing_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *PacingMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}

func (m *PacingMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value)
	}
	on := m.enabled != nil && m.enabled()
	emit("enabled", boolMetric(on))
	if !on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	stats, err := m.source.PacingStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("cooling_domains", float64(stats.CoolingDomains))
	emit("waiting_recipients", float64(stats.WaitingRecipients))
}
