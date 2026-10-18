package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type SchedulingStatistics struct {
	ScheduledWaiting int64
	ExpiringPending  int64
}

func (s *PGStore) SchedulingStatistics(ctx context.Context) (SchedulingStatistics, error) {
	r, err := s.queries.SchedulingStatistics(ctx, s.pool, s.namespace)
	return SchedulingStatistics{ScheduledWaiting: r.ScheduledWaiting, ExpiringPending: r.ExpiringPending}, err
}

type SchedulingStatisticsReader interface {
	SchedulingStatistics(context.Context) (SchedulingStatistics, error)
}

type SchedulingMonitor struct {
	source  SchedulingStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewSchedulingMonitor(source SchedulingStatisticsReader, enabled func() bool) *SchedulingMonitor {
	m := &SchedulingMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal timed admission and dispatch gate."},
		{"stats_up", "Whether durable scheduling aggregates were read successfully."},
		{"scheduled_waiting", "Queued recipients whose scheduled time has not arrived."},
		{"expiring_pending", "Maild-owned recipients with an explicit expiry deadline."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_scheduling_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *SchedulingMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}

func (m *SchedulingMonitor) Collect(ch chan<- prometheus.Metric) {
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
	stats, err := m.source.SchedulingStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("scheduled_waiting", float64(stats.ScheduledWaiting))
	emit("expiring_pending", float64(stats.ExpiringPending))
}
