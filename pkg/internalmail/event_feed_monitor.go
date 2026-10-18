package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type EventFeedStatistics struct {
	Unpublished    int64
	Published      int64
	Consumers      int64
	PendingBatches int64
	Unacked        int64
	MaxBacklog     int64
	OldestSeconds  float64
}
type eventFeedStatisticsReader interface {
	eventFeedStatistics(context.Context) (EventFeedStatistics, error)
}

func (s *PGStore) eventFeedStatistics(ctx context.Context) (EventFeedStatistics, error) {
	r, err := s.queries.EventFeedStats(ctx, s.pool, s.namespace)
	return EventFeedStatistics{Unpublished: r.Unpublished, Published: r.Published, Consumers: r.Consumers, PendingBatches: r.PendingBatches, Unacked: r.Unacked, MaxBacklog: r.MaxBacklog, OldestSeconds: r.OldestSeconds}, err
}

type EventFeedMonitor struct {
	source  eventFeedStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewEventFeedMonitor(source eventFeedStatisticsReader, enabled func() bool) *EventFeedMonitor {
	m := &EventFeedMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal delivery event feed gate."},
		{"stats_up", "Whether durable event feed statistics were read successfully."},
		{"unpublished_events", "Committed named-producer events waiting for publication."},
		{"published_events", "Durable published delivery observations."},
		{"consumers", "Registered producer event consumers."},
		{"pending_batches", "Consumer batches awaiting acknowledgment."},
		{"unacked_events", "Unacknowledged events summed across independent consumers."},
		{"max_consumer_backlog", "Largest consumer backlog in events."},
		{"oldest_unacked_seconds", "Age since publication of the oldest unacknowledged event."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_event_feed_"+d.name, d.help, nil, nil)
	}
	return m
}
func (m *EventFeedMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}
func (m *EventFeedMonitor) Collect(ch chan<- prometheus.Metric) {
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
	stats, err := m.source.eventFeedStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	for name, value := range map[string]float64{"unpublished_events": float64(stats.Unpublished), "published_events": float64(stats.Published), "consumers": float64(stats.Consumers), "pending_batches": float64(stats.PendingBatches), "unacked_events": float64(stats.Unacked), "max_consumer_backlog": float64(stats.MaxBacklog), "oldest_unacked_seconds": stats.OldestSeconds} {
		emit(name, value)
	}
}
