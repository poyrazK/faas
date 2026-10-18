package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type SuppressionControlStatistics struct {
	ManualBlocks      int64
	BounceBlocks      int64
	ComplaintBlocks   int64
	ReleasedAddresses int64
}

func (s *PGStore) suppressionControlStatistics(ctx context.Context) (SuppressionControlStatistics, error) {
	r, err := s.queries.SuppressionControlStatistics(ctx, s.pool, s.namespace)
	return SuppressionControlStatistics{ManualBlocks: r.ManualBlocks, BounceBlocks: r.BounceBlocks, ComplaintBlocks: r.ComplaintBlocks, ReleasedAddresses: r.ReleasedAddresses}, err
}

type suppressionStatisticsReader interface {
	suppressionControlStatistics(context.Context) (SuppressionControlStatistics, error)
}

type SuppressionControlMonitor struct {
	source  suppressionStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewSuppressionControlMonitor(source suppressionStatisticsReader, enabled func() bool) *SuppressionControlMonitor {
	m := &SuppressionControlMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal suppression management gate."},
		{"stats_up", "Whether durable suppression aggregates were read successfully."},
		{"manual_blocks", "Currently blocked addresses with manual reason."},
		{"bounce_blocks", "Currently blocked addresses with hard bounce reason."},
		{"complaint_blocks", "Currently blocked addresses with protected complaint reason."},
		{"released_addresses", "Addresses with a current reviewed release and retained suppression history."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_suppression_controls_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *SuppressionControlMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.desc {
		ch <- desc
	}
}

func (m *SuppressionControlMonitor) Collect(ch chan<- prometheus.Metric) {
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
	stats, err := m.source.suppressionControlStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("manual_blocks", float64(stats.ManualBlocks))
	emit("bounce_blocks", float64(stats.BounceBlocks))
	emit("complaint_blocks", float64(stats.ComplaintBlocks))
	emit("released_addresses", float64(stats.ReleasedAddresses))
}
