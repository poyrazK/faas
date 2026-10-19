package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

func (s *PGStore) DeliveryControlStatistics(ctx context.Context) (map[string]int64, error) {
	rows, err := s.queries.DeliveryControlsStatistics(ctx, s.pool, s.namespace)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, r := range rows {
		counts[r.Mode] = r.Total
	}
	return counts, nil
}

type DeliveryControlStatisticsReader interface {
	DeliveryControlStatistics(context.Context) (map[string]int64, error)
}
type DeliveryControlMonitor struct {
	source  DeliveryControlStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewDeliveryControlMonitor(source DeliveryControlStatisticsReader, enabled func() bool) *DeliveryControlMonitor {
	m := &DeliveryControlMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal operator delivery controls gate."},
		{"stats_up", "Whether persisted delivery control aggregates were read successfully."},
		{"paused_scopes", "Persisted paused namespace or domain scopes, without identifiers."},
		{"draining_scopes", "Persisted draining namespace or domain scopes, without identifiers."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_controls_"+d.name, d.help, nil, nil)
	}
	return m
}
func (m *DeliveryControlMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}
func (m *DeliveryControlMonitor) Collect(ch chan<- prometheus.Metric) {
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
	counts, err := m.source.DeliveryControlStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("paused_scopes", float64(counts["paused"]))
	emit("draining_scopes", float64(counts["draining"]))
}
