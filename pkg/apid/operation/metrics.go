// adr: 20261016123221
package operation

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// Metrics counts verified provider events on operation-bound endpoints.
type Metrics struct {
	registry      *prometheus.Registry
	webhookEvents *prometheus.CounterVec
}

func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	events := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_customer_operation_webhook_events_total",
		Help: "Verified provider events on operation-bound endpoints by binding mode and result; refused is any non-2xx answer.",
	}, []string{"mode", "result"})
	if err := registry.Register(events); err != nil {
		var existing prometheus.AlreadyRegisteredError
		if !errors.As(err, &existing) {
			panic(err)
		}
		events = existing.ExistingCollector.(*prometheus.CounterVec)
	}
	return &Metrics{registry: registry, webhookEvents: events}
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
