package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type RecoveryMonitor struct {
	source interface {
		recoveryStatus(context.Context) (RecoveryStatus, error)
	}
	enabled func() bool
	desc    map[string]*prometheus.Desc
}

func NewRecoveryMonitor(source interface {
	recoveryStatus(context.Context) (RecoveryStatus, error)
}, enabled func() bool) *RecoveryMonitor {
	m := &RecoveryMonitor{source: source, enabled: enabled, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal recovery management gate."},
		{"stats_up", "Whether durable recovery status was read successfully."},
		{"held", "Whether database recovery blocks admission and new handoffs."},
		{"quarantined", "Pending recipients from an older unreviewed recovery generation."},
		{"active_submissions", "Active SMTP custody leases, preserved during recovery."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_recovery_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *RecoveryMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.desc {
		ch <- desc
	}
}

func (m *RecoveryMonitor) Collect(ch chan<- prometheus.Metric) {
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
	status, err := m.source.recoveryStatus(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("held", boolMetric(status.Held))
	emit("quarantined", float64(status.Quarantined))
	emit("active_submissions", float64(status.ActiveSubmissions))
}
