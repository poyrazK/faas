package internalmail

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
	"github.com/prometheus/client_golang/prometheus"
)

type DomainLifecycleStatistics struct {
	Paused, Stale, Unavailable, Staged, FailedKeys int64
	OldestStagedSeconds                            float64
}

func (s *PGStore) DomainLifecycleStatistics(ctx context.Context, ip string) (DomainLifecycleStatistics, error) {
	r, err := s.queries.DomainLifecycleStatistics(ctx, s.pool, q.DomainLifecycleStatisticsParams{Namespace: s.namespace, SendingIp: ip, MaxAgeSeconds: int32(api.InternalMailDomainCheckMaxAge / time.Second)})
	return DomainLifecycleStatistics{r.Paused, r.Stale, r.Unavailable, r.Staged, r.FailedKeys, r.OldestStagedSeconds}, err
}

type DomainLifecycleStatisticsReader interface {
	DomainLifecycleStatistics(context.Context, string) (DomainLifecycleStatistics, error)
}

// All labels are fixed. Signing material, selectors, domains and addresses
// never enter metrics. Disabled collectors read no persistent state.
type DomainLifecycleMonitor struct {
	source             DomainLifecycleStatisticsReader
	rotation, reverify func() bool
	ip                 string
	desc               map[string]*prometheus.Desc
	mu                 sync.Mutex
	lastAttempt        float64
	lastUp             bool
}

func NewDomainLifecycleMonitor(source DomainLifecycleStatisticsReader, rotation, reverify func() bool, ip string) *DomainLifecycleMonitor {
	m := &DomainLifecycleMonitor{source: source, rotation: rotation, reverify: reverify, ip: ip, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"dkim_rotation_enabled", "Internal DKIM rotation gate."},
		{"domain_reverify_enabled", "Internal recurring domain verification gate."},
		{"domain_lifecycle_stats_up", "Whether durable domain and key statistics were read successfully."},
		{"dkim_staged_keys", "Durable staged signing key count."},
		{"dkim_failed_keys", "Staged signing keys whose most recent DNS check failed."},
		{"dkim_oldest_staged_seconds", "Age of the oldest staged signing key."},
		{"domain_paused", "Verified domains with confirmed authorization failure."},
		{"domain_stale", "Verified domains without sufficiently fresh authorization for the configured source IP."},
		{"domain_lookup_unavailable", "Verified domains whose last DNS lookup was unavailable."},
		{"domain_check_last_attempt_seconds", "Last completed periodic domain pass; resets on restart."},
		{"domain_check_last_run_up", "Whether the periodic domain pass completed successfully."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_"+d.name, d.help, nil, nil)
	}
	return m
}
func (m *DomainLifecycleMonitor) Observe(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAttempt = float64(time.Now().Unix())
	m.lastUp = err == nil
}
func (m *DomainLifecycleMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}
func (m *DomainLifecycleMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value)
	}
	rotation, reverify := m.rotation != nil && m.rotation(), m.reverify != nil && m.reverify()
	emit("dkim_rotation_enabled", boolMetric(rotation))
	emit("domain_reverify_enabled", boolMetric(reverify))
	if !rotation && !reverify {
		return
	}
	if reverify {
		m.mu.Lock()
		attempt, up := m.lastAttempt, m.lastUp
		m.mu.Unlock()
		emit("domain_check_last_attempt_seconds", attempt)
		emit("domain_check_last_run_up", boolMetric(up))
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	stats, err := m.source.DomainLifecycleStatistics(ctx, m.ip)
	emit("domain_lifecycle_stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	if rotation {
		emit("dkim_staged_keys", float64(stats.Staged))
		emit("dkim_failed_keys", float64(stats.FailedKeys))
		emit("dkim_oldest_staged_seconds", stats.OldestStagedSeconds)
	}
	if reverify {
		emit("domain_paused", float64(stats.Paused))
		emit("domain_stale", float64(stats.Stale))
		emit("domain_lookup_unavailable", float64(stats.Unavailable))
	}
}
