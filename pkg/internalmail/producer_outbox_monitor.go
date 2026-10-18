package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type ProducerOutboxMonitor struct {
	source interface {
		producerOutboxCounts(context.Context) ([]ProducerOutboxCount, error)
	}
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewProducerOutboxMonitor(source interface {
	producerOutboxCounts(context.Context) ([]ProducerOutboxCount, error)
}, enabled func() bool) *ProducerOutboxMonitor {
	m := &ProducerOutboxMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, name := range []string{"enabled", "stats_up", "queued", "claimed", "admitted", "failed", "expired", "cancelled", "suppressed", "recovery_held"} {
		m.desc[name] = prometheus.NewDesc("maild_mail_producer_outbox_"+name, "Private producer handoff "+name+" aggregate.", nil, nil)
	}
	return m
}
func (m *ProducerOutboxMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}
func (m *ProducerOutboxMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, v float64) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, v)
	}
	on := m.enabled != nil && m.enabled()
	emit("enabled", boolMetric(on))
	if !on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	counts, err := m.source.producerOutboxCounts(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	totals := map[string]int64{}
	for _, c := range counts {
		totals[c.State] += c.Total
		totals["recovery_held"] += c.RecoveryHeld
	}
	for _, state := range []string{"queued", "claimed", "admitted", "failed", "expired", "cancelled", "suppressed", "recovery_held"} {
		emit(state, float64(totals[state]))
	}
}
