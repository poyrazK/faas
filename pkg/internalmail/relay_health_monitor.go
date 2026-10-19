package internalmail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type relayStatusReader interface {
	relayStatus(context.Context) (RelayStatus, error)
}

type RelayHealthMonitor struct {
	source  relayStatusReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewRelayHealthMonitor(source relayStatusReader, enabled func() bool) *RelayHealthMonitor {
	m := &RelayHealthMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal relay backpressure gate."},
		{"stats_up", "Whether durable relay health was read successfully."},
		{"held", "Whether automatic relay protection holds new maild handoffs."},
		{"sample_fresh", "Whether the last relay sample is fresh."},
		{"sample_age_seconds", "Age of the last observed relay sample."},
		{"queue_messages", "Last observed whole-relay queue count."},
		{"oldest_seconds", "Last observed age of the oldest whole-relay message."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_relay_"+d.name, d.help, nil, nil)
	}
	m.desc["hold_reason"] = prometheus.NewDesc("maild_mail_relay_hold_reason", "Closed reason for automatic relay protection.", []string{"reason"}, nil)
	return m
}

func (m *RelayHealthMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}

func (m *RelayHealthMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value, labels...)
	}
	on := m.enabled != nil && m.enabled()
	emit("enabled", boolMetric(on))
	if !on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	status, err := m.source.relayStatus(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("held", boolMetric(status.Held))
	emit("sample_fresh", boolMetric(status.Fresh))
	for _, reason := range []string{"unobserved", "stale", "inventory_unavailable", "smtp_unavailable", "queue_depth", "queue_age", "recovering", "healthy"} {
		emit("hold_reason", boolMetric(status.Reason == reason), reason)
	}
	if o := status.Observation; o != nil {
		emit("sample_age_seconds", max(0, time.Since(o.ObservedAt).Seconds()))
		if status.Fresh && o.InventoryOK {
			emit("queue_messages", float64(o.QueueMessages))
			emit("oldest_seconds", float64(o.OldestSeconds))
		}
	}
}
