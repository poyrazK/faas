package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type ProducerSchedulerMonitor struct {
	service *Service
	desc    map[string]*prometheus.Desc
}

func NewProducerSchedulerMonitor(service *Service) *ProducerSchedulerMonitor {
	m := &ProducerSchedulerMonitor{service: service, desc: map[string]*prometheus.Desc{}}
	for _, d := range []struct{ name, help string }{
		{"enabled", "Internal fair producer scheduling gate."},
		{"stats_up", "Whether producer dispatch aggregates were read successfully."},
		{"limited_producers", "Enrolled producer buckets at their rolling dispatch limit, including legacy."},
		{"reservations", "Committed submission reservations in the rolling minute."},
		{"due_recipients", "Due queued recipients, including recipients held by other delivery controls."},
		{"rate_limited_recipients", "Due queued recipients in producer buckets at their dispatch limit."},
		{"oldest_due_seconds", "Greatest queue age since message due time across producer buckets."},
	} {
		m.desc[d.name] = prometheus.NewDesc("maild_mail_producer_scheduler_"+d.name, d.help, nil, nil)
	}
	return m
}

func (m *ProducerSchedulerMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}

func (m *ProducerSchedulerMonitor) read(ctx context.Context) ([]ProducerDispatchStatus, error) {
	policy, err := m.service.dispatchPolicy()
	if err != nil {
		return nil, err
	}
	store, ok := m.service.Store.(producerSchedulerStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store.producerDispatchStatus(ctx, policy)
}

func (m *ProducerSchedulerMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value)
	}
	on := m.service.producerSchedulingEnabled()
	emit("enabled", boolMetric(on))
	if !on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	rows, err := m.read(ctx)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	var limited, reservations, due, waiting, oldest float64
	for _, row := range rows {
		reservations += float64(row.InWindow)
		due += float64(row.DueRecipients)
		oldest = max(oldest, row.OldestDueSeconds)
		if row.RateLimited {
			limited++
			waiting += float64(row.DueRecipients)
		}
	}
	emit("limited_producers", limited)
	emit("reservations", reservations)
	emit("due_recipients", due)
	emit("rate_limited_recipients", waiting)
	emit("oldest_due_seconds", oldest)
}
