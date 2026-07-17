// Prometheus instrumentation for gatewayd (spec §4.1, §12). The metric names
// here are dashboard dependencies — DO NOT rename without coordinating with
// the dashboards in deploy/grafana/. We register on a per-Handler registry
// (not the global default) so concurrent tests don't collide.
//
// Emitted series:
//   - gateway_requests_total{app, plan, code}        counter
//   - gateway_wake_latency_seconds                    histogram
//   - gateway_queue_depth{app}                       gauge (set/cleared by
//     WakeGate.SetGaugeSink)
//   - gateway_rate_limited_total{app, plan}          counter
//   - gateway_cold_wake_total{app}                   counter
package gateway

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Metrics is the gatewayd Prometheus bundle. Construct once per Handler via
// NewMetrics and pass into NewHandlerWith.
type Metrics struct {
	registry *prometheus.Registry

	requests    *prometheus.CounterVec
	wakeLatency prometheus.Histogram
	queueDepth  *prometheus.GaugeVec
	rateLimited *prometheus.CounterVec
	coldWake    *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total gateway requests, labelled by app, plan, and HTTP status class.",
		}, []string{"app", "plan", "code"}),
		wakeLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "gateway_wake_latency_seconds",
			Help: "End-to-end latency from request received to first upstream byte after a cold wake.",
			// Buckets target the §12 SLO: p50 ≤ 0.35 s, p95 ≤ 0.8 s, page > 1.5 s.
			Buckets: []float64{
				0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0,
			},
		}),
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_queue_depth",
			Help: "Current number of waiters per app's wake queue (sampled).",
		}, []string{"app"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_rate_limited_total",
			Help: "Requests rejected by the per-app rate limiter.",
		}, []string{"app", "plan"}),
		coldWake: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_cold_wake_total",
			Help: "Requests that triggered a cold wake for an app.",
		}, []string{"app"}),
	}
	reg.MustRegister(m.requests, m.wakeLatency, m.queueDepth, m.rateLimited, m.coldWake)
	return m
}

// Registry returns the underlying *prometheus.Registry — pass to promhttp.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler returns an http.Handler that serves the registry's metrics in the
// Prometheus text exposition format. Mount at /metrics on the control listener.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// ObserveRequest records a completed request's outcome. code is the HTTP
// status class as a 3-digit string ("200", "404", "503"...).
func (m *Metrics) ObserveRequest(appID, plan, code string) {
	m.requests.WithLabelValues(appID, plan, code).Inc()
}

// ObserveRateLimit records a 429 outcome.
func (m *Metrics) ObserveRateLimit(appID, plan string) {
	m.rateLimited.WithLabelValues(appID, plan).Inc()
}

// ObserveColdWake records that this request caused a cold wake and observes
// the wake latency (request-received to first upstream byte).
func (m *Metrics) ObserveColdWake(appID string, latency time.Duration) {
	m.coldWake.WithLabelValues(appID).Inc()
	m.wakeLatency.Observe(latency.Seconds())
}

// SetQueueDepth records the current wake-queue depth for an app.
func (m *Metrics) SetQueueDepth(appID string, depth int) {
	m.queueDepth.WithLabelValues(appID).Set(float64(depth))
}

// requestLogger is a one-line structured slog request logger used by Handler.
// Built as a type so tests can replace WithLogger.
type requestLogger struct{ log *slog.Logger }

func (l *requestLogger) Log(appID, code string, latency time.Duration, cold bool, requestID string) {
	if l == nil || l.log == nil {
		return
	}
	// requestID flows from the x-faas-request-id HTTP header (pkg/gateway/observability.go:requestIDFrom)
	// and is therefore attacker-controllable. Strip CR/LF/NUL/DEL before logging so a forged
	// header cannot smuggle a new log line into the stream. appID and code are server-generated
	// (UUIDs / HTTP status class digit) and need no sanitization.
	//
	// codeql[go/log-injection] false-positive: logsanitize.Field is not in CodeQL's sanitizer model
	// (the query only recognizes inline strings.ReplaceAll), but it does strip the injection bytes
	// at runtime — matching the defense-in-depth precedent set for the synth RPC (47d5531).
	l.log.Info("gateway_request",
		"app_id", appID,
		"code", code,
		"latency_ms", latency.Milliseconds(),
		"cold", cold,
		"request_id", logsanitize.Field(requestID),
	)
}
