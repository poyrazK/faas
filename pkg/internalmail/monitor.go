package internalmail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

const MonitoringPath = APIPath + "/monitoring"

type StateStat struct {
	Count           int64
	OldestSeconds   float64
	StateAgeSeconds float64
}

type Statistics struct {
	Recipients   map[string]StateStat
	Events       map[string]int64
	Outcomes     map[string]int64
	Suppressions int64
}

func (s *PGStore) Statistics(ctx context.Context) (Statistics, error) {
	rows, err := s.queries.MonitoringStats(ctx, s.pool, s.namespace)
	if err != nil {
		return Statistics{}, err
	}
	stats := Statistics{Recipients: map[string]StateStat{}, Events: map[string]int64{}, Outcomes: map[string]int64{}}
	for _, row := range rows {
		switch row.Category {
		case "recipient":
			stats.Recipients[row.Label] = StateStat{row.Total, row.OldestSeconds, row.StateAgeSeconds}
		case "event":
			stats.Events[row.Label] = row.Total
		case "outcome":
			stats.Outcomes[row.Label] = row.Total
		case "suppression":
			stats.Suppressions = row.Total
		}
	}
	return stats, nil
}

type StatisticsReader interface {
	Statistics(context.Context) (Statistics, error)
}

// Monitor reads durable state at collection time, so restarts do not reset
// counts or erase a backlog. All labels come from fixed state sets; addresses,
// message IDs, domains, credentials and provider diagnostics are excluded.
type Monitor struct {
	source                     StatisticsReader
	enabled, delivery, reports func() bool
	feedback                   func() bool
	desc                       map[string]*prometheus.Desc
}

func NewMonitor(source StatisticsReader, enabled, delivery, reports, feedback func() bool) *Monitor {
	m := &Monitor{source: source, enabled: enabled, delivery: delivery, reports: reports, feedback: feedback, desc: map[string]*prometheus.Desc{}}
	for _, def := range []struct {
		name, help string
		labels     []string
	}{
		{"enabled", "Internal mail master gate.", nil},
		{"delivery_enabled", "Internal mail SMTP dispatch gate.", nil},
		{"reports_enabled", "Internal mail automatic reports gate.", nil},
		{"feedback_enabled", "Internal mail authenticated provider feedback gate.", nil},
		{"stats_up", "Whether the durable namespace statistics were read successfully.", nil},
		{"recipients", "Durable recipient count by current state.", []string{"state"}},
		{"oldest_pending_seconds", "Age of the oldest mail still owned by maild.", nil},
		{"oldest_report_wait_seconds", "Time since relay submission for the oldest recipient awaiting a final report.", nil},
		{"events_last_day", "Durable events recorded in the last 24 hours, including after restarts.", []string{"kind"}},
		{"outcomes_last_day", "Distinct recipients whose current terminal state was recorded in the last 24 hours.", []string{"kind"}},
		{"suppressions", "Current namespace mailbox suppressions.", nil},
	} {
		m.desc[def.name] = prometheus.NewDesc("maild_mail_"+def.name, def.help, def.labels, nil)
	}
	return m
}

func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.desc {
		ch <- desc
	}
}

func (m *Monitor) emit(ch chan<- prometheus.Metric, name string, value float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value, labels...)
}

func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	if m.enabled == nil || !m.enabled() {
		m.emit(ch, "enabled", 0)
		return
	}
	m.emit(ch, "enabled", 1)
	m.emit(ch, "delivery_enabled", boolMetric(m.delivery != nil && m.delivery()))
	reports := m.reports != nil && m.reports()
	m.emit(ch, "reports_enabled", boolMetric(reports))
	m.emit(ch, "feedback_enabled", boolMetric(m.feedback != nil && m.feedback()))
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	stats, err := m.source.Statistics(ctx)
	m.emit(ch, "stats_up", boolMetric(err == nil))
	if err == nil {
		m.collectState(ch, stats)
	}
}

func (m *Monitor) collectState(ch chan<- prometheus.Metric, stats Statistics) {
	oldest := float64(0)
	for _, state := range []string{"queued", "claimed", "submitting", "deferred", "uncertain", "submitted", "accepted", "bounced", "complained", "suppressed", "failed", "cancelled", "expired"} {
		stat := stats.Recipients[state]
		m.emit(ch, "recipients", float64(stat.Count), state)
		switch state {
		case "queued", "claimed", "submitting", "deferred", "uncertain":
			oldest = max(oldest, stat.OldestSeconds)
		}
	}
	m.emit(ch, "oldest_pending_seconds", oldest)
	m.emit(ch, "oldest_report_wait_seconds", stats.Recipients["submitted"].StateAgeSeconds)
	m.emit(ch, "suppressions", float64(stats.Suppressions))
	for _, kind := range []string{"queued", "submitted", "accepted", "bounced", "complained", "deferred", "suppressed", "failed", "uncertain", "retry", "cancelled", "expired"} {
		m.emit(ch, "events_last_day", float64(stats.Events[kind]), kind)
	}
	for _, kind := range []string{"accepted", "bounced", "failed", "complained"} {
		m.emit(ch, "outcomes_last_day", float64(stats.Outcomes[kind]), kind)
	}
}

func boolMetric(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func oldestRelayAge(arrival int64, now time.Time) float64 {
	return max(0, now.Sub(time.Unix(arrival, 0)).Seconds())
}
