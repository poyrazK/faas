package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type ProducerLifecycleStatistics struct {
	SuspendedProducers  int64
	RevokedCredentials  int64
	RetiringCredentials int64
	ExpiredCredentials  int64
}

func (s *PGStore) producerLifecycleStatistics(ctx context.Context) (ProducerLifecycleStatistics, error) {
	r, err := s.queries.ProducerLifecycleStatistics(ctx, s.pool, s.namespace)
	return ProducerLifecycleStatistics{SuspendedProducers: r.SuspendedProducers, RevokedCredentials: r.RevokedCredentials, RetiringCredentials: r.RetiringCredentials, ExpiredCredentials: r.ExpiredCredentials}, err
}

type producerLifecycleStatisticsReader interface {
	producerLifecycleStatistics(context.Context) (ProducerLifecycleStatistics, error)
}

type ProducerLifecycleMonitor struct {
	source  producerLifecycleStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewProducerLifecycleMonitor(source producerLifecycleStatisticsReader, enabled func() bool) *ProducerLifecycleMonitor {
	m := &ProducerLifecycleMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal producer credential lifecycle management gate."},
		{"stats_up", "Whether durable producer access aggregates were read successfully."},
		{"suspended_producers", "Producer identities with a durable suspension."},
		{"revoked_credentials", "Permanently revoked producer credential identifiers."},
		{"retiring_credentials", "Unrevoked producer credentials inside their saved overlap window."},
		{"expired_credentials", "Unrevoked producer credentials past their irreversible retirement deadline."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_producer_access_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *ProducerLifecycleMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}

func (m *ProducerLifecycleMonitor) Collect(ch chan<- prometheus.Metric) {
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
	stats, err := m.source.producerLifecycleStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("suspended_producers", float64(stats.SuspendedProducers))
	emit("revoked_credentials", float64(stats.RevokedCredentials))
	emit("retiring_credentials", float64(stats.RetiringCredentials))
	emit("expired_credentials", float64(stats.ExpiredCredentials))
}
