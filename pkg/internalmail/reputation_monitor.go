package internalmail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
	"github.com/prometheus/client_golang/prometheus"
)

type ReputationStatistics struct {
	Holds    map[string]map[string]int64
	Feedback map[string]int64
}
type reputationStatisticsReader interface {
	reputationStatistics(context.Context) (ReputationStatistics, error)
}

func (s *PGStore) reputationStatistics(ctx context.Context) (ReputationStatistics, error) {
	rows, err := s.queries.ReputationStats(ctx, s.pool, q.ReputationStatsParams{Namespace: s.namespace, WindowSeconds: int32(api.InternalMailReputationWindow / time.Second)})
	stats := ReputationStatistics{Holds: map[string]map[string]int64{"domain": {}, "producer": {}}, Feedback: map[string]int64{}}
	for _, r := range rows {
		if r.Category == "hold" {
			stats.Holds[r.Label][r.Detail] = r.Total
		} else {
			stats.Feedback[r.Label] = r.Total
		}
	}
	return stats, err
}

type ReputationMonitor struct {
	source  reputationStatisticsReader
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewReputationMonitor(source reputationStatisticsReader, enabled func() bool) *ReputationMonitor {
	m := &ReputationMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	m.desc["enabled"] = prometheus.NewDesc("maild_mail_reputation_enabled", "Internal reputation protection gate.", nil, nil)
	m.desc["stats_up"] = prometheus.NewDesc("maild_mail_reputation_stats_up", "Whether durable reputation statistics were read successfully.", nil, nil)
	m.desc["holds"] = prometheus.NewDesc("maild_mail_reputation_holds", "Durable sender holds awaiting operator review.", []string{"scope", "reason"}, nil)
	m.desc["feedback_recipients"] = prometheus.NewDesc("maild_mail_reputation_feedback_recipients", "Distinct authenticated adverse recipients first observed in the last 24 hours; complaints take priority.", []string{"kind"}, nil)
	return m
}
func (m *ReputationMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}
func (m *ReputationMonitor) Collect(ch chan<- prometheus.Metric) {
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
	stats, err := m.source.reputationStatistics(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	for _, scope := range []string{"domain", "producer"} {
		for _, reason := range []string{"bounce_rate", "complaint_rate"} {
			emit("holds", float64(stats.Holds[scope][reason]), scope, reason)
		}
	}
	for _, kind := range []string{"bounced", "complained"} {
		emit("feedback_recipients", float64(stats.Feedback[kind]), kind)
	}
}
