// adr: 1285
package operation

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// Metrics counts verified provider events on operation-bound endpoints.
type Metrics struct {
	registry      *prometheus.Registry
	webhookEvents *prometheus.CounterVec
	scheduleFires *prometheus.CounterVec
	queueReleases prometheus.Counter
	queued        prometheus.Gauge
	queueOldest   prometheus.Gauge
	childStarts   *prometheus.CounterVec
	fanInSignals  *prometheus.CounterVec
	fanInOldest   prometheus.Gauge
}

func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	events := registerCounterVec(registry, prometheus.CounterOpts{
		Name: prefix + "_customer_operation_webhook_events_total",
		Help: "Verified provider events on operation-bound endpoints by binding mode and result; refused is any non-2xx answer.",
	}, "mode", "result")
	fires := registerCounterVec(registry, prometheus.CounterOpts{
		Name: prefix + "_customer_operation_schedule_fires_total",
		Help: "Operation schedule fires by result: admitted, refused (recorded on the schedule) or error (retried after the lease lapses).",
	}, "result")
	releases := registerCounterVec(registry, prometheus.CounterOpts{
		Name: prefix + "_customer_operation_queue_releases_total",
		Help: "Queued Operations started by the per-customer concurrency releaser.",
	}).WithLabelValues()
	queued := registerGauge(registry, prometheus.GaugeOpts{
		Name: prefix + "_customer_operation_queued",
		Help: "Operations held by per-customer concurrency policies, fleet-wide; every apid reports the same value.",
	})
	oldest := registerGauge(registry, prometheus.GaugeOpts{
		Name: prefix + "_customer_operation_queue_oldest_seconds",
		Help: "Age of the oldest Operation held by a per-customer concurrency policy; 0 when none is queued.",
	})
	childStarts := registerCounterVec(registry, prometheus.CounterOpts{
		Name: prefix + "_customer_operation_child_starts_total",
		Help: "Child Operation starts by parent execution (http, job, workflow) and result: started, replayed (the key already named a child) or refused.",
	}, "parent", "result")
	fanInSignals := registerCounterVec(registry, prometheus.CounterOpts{
		Name: prefix + "_customer_operation_fan_in_signals_total",
		Help: "operation.children.settled signals by result: signaled, skipped (the parent no longer waits) or error (retried on the next pass).",
	}, "result")
	fanInOldest := registerGauge(registry, prometheus.GaugeOpts{
		Name: prefix + "_customer_operation_fan_in_oldest_seconds",
		Help: "Time since the oldest parent waiting on operation.children.settled had every child settled, at the start of the latest signal pass; 0 when none.",
	})
	return &Metrics{registry: registry, webhookEvents: events, scheduleFires: fires, queueReleases: releases, queued: queued, queueOldest: oldest,
		childStarts: childStarts, fanInSignals: fanInSignals, fanInOldest: fanInOldest}
}

func registerGauge(registry *prometheus.Registry, opts prometheus.GaugeOpts) prometheus.Gauge {
	gauge := prometheus.NewGauge(opts)
	if err := registry.Register(gauge); err != nil {
		var existing prometheus.AlreadyRegisteredError
		if !errors.As(err, &existing) {
			panic(err)
		}
		gauge = existing.ExistingCollector.(prometheus.Gauge)
	}
	return gauge
}

func (m *Metrics) operationQueue(queued int64, oldest *time.Time, now time.Time) {
	if m == nil {
		return
	}
	m.queued.Set(float64(queued))
	age := 0.0
	if oldest != nil && now.After(*oldest) {
		age = now.Sub(*oldest).Seconds()
	}
	m.queueOldest.Set(age)
}

func registerCounterVec(registry *prometheus.Registry, opts prometheus.CounterOpts, labels ...string) *prometheus.CounterVec {
	counter := prometheus.NewCounterVec(opts, labels)
	if err := registry.Register(counter); err != nil {
		var existing prometheus.AlreadyRegisteredError
		if !errors.As(err, &existing) {
			panic(err)
		}
		counter = existing.ExistingCollector.(*prometheus.CounterVec)
	}
	return counter
}

func (m *Metrics) operationQueueReleases(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.queueReleases.Add(float64(n))
}

func (m *Metrics) childStart(parent, result string) {
	if m == nil {
		return
	}
	m.childStarts.WithLabelValues(parent, result).Inc()
}

func (m *Metrics) fanInSignal(result string) {
	if m == nil {
		return
	}
	m.fanInSignals.WithLabelValues(result).Inc()
}

// fanIn reports how long the oldest ready parent has waited for its signal.
func (m *Metrics) fanIn(oldest *time.Time, now time.Time) {
	if m == nil {
		return
	}
	age := 0.0
	if oldest != nil && now.After(*oldest) {
		age = now.Sub(*oldest).Seconds()
	}
	m.fanInOldest.Set(age)
}

func (m *Metrics) scheduleFire(result string) {
	if m == nil {
		return
	}
	m.scheduleFires.WithLabelValues(result).Inc()
}

func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *Metrics) webhookEvent(mode, result string) {
	if m == nil {
		return
	}
	m.webhookEvents.WithLabelValues(mode, result).Inc()
}

// webhookResults bounds the result label to the receipt statuses.
var webhookResults = map[string]bool{"accepted": true, "ignored": true, api.InboundWebhookSignaled: true, api.InboundWebhookUnmatched: true}

// webhookOutcomeWriter keeps the status and the start of the body, enough
// to read the receipt the handler answered with.
type webhookOutcomeWriter struct {
	http.ResponseWriter
	status int
	body   []byte
}

const webhookOutcomeBodyBytes = 1024

func (w *webhookOutcomeWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *webhookOutcomeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if room := webhookOutcomeBodyBytes - len(w.body); room > 0 {
		w.body = append(w.body, b[:min(len(b), room)]...)
	}
	return w.ResponseWriter.Write(b)
}

func (w *webhookOutcomeWriter) result() string {
	if w.status < 200 || w.status > 299 {
		return "refused"
	}
	var receipt api.InboundWebhookReceiptResponse
	if json.Unmarshal(w.body, &receipt) != nil || !webhookResults[receipt.Status] {
		return "accepted"
	}
	if receipt.Duplicate {
		return "duplicate"
	}
	return receipt.Status
}
