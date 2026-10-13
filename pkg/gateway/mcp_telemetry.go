package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/prometheus/client_golang/prometheus"
)

func (m *Metrics) initMCPMetrics(reg prometheus.Registerer) {
	m.mcpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gateway_mcp_requests_total", Help: "Completed requests with MCP headers, by app, claimed RPC method and transport outcome."}, []string{"app", "method", "outcome"})
	m.mcpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gateway_mcp_request_duration_seconds", Help: "Ingress-to-completion duration for requests with MCP headers, including wake and stream lifetime.", Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300}}, []string{"app", "method"})
	m.mcpActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gateway_mcp_active_forwarding_streams", Help: "Active streaming-forwarder requests with MCP headers; includes JSON responses on the streaming path."}, []string{"app", "method"})
	reg.MustRegister(m.mcpRequests, m.mcpDuration, m.mcpActive)
}
func mcpTelemetryMethod(r *http.Request) string {
	if r.Header.Get("MCP-Method") == "" && r.Header.Get("MCP-Protocol-Version") == "" {
		return ""
	}
	return api.MCPMethodLabel(r.Header.Get("MCP-Method"))
}
func (h *Handler) observeMCPRequest(r *http.Request, status int, appID string, elapsed time.Duration) {
	method := mcpTelemetryMethod(r)
	if method == "" || appID == "" || appID == "-" || isRouteProbe(r.Context()) {
		return
	}
	outcome := api.MCPHTTPOutcome(status)
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		outcome = "timeout"
	} else if errors.Is(r.Context().Err(), context.Canceled) {
		outcome = "canceled"
	}
	if h.metrics != nil {
		h.metrics.mcpRequests.WithLabelValues(appID, method, outcome).Inc()
		h.metrics.mcpDuration.WithLabelValues(appID, method).Observe(elapsed.Seconds())
	}
	if outcome != "http_success" && h.log != nil {
		h.log.Info("MCP request transport failure", "app_id", appID, "method", method, "outcome", outcome, "status", status, "duration_ms", elapsed.Milliseconds(), "request_id", logsanitize.Field(requestIDFrom(r)), "trace_id", traceIDForTelemetry(r.Context()))
	}
}
func (h *Handler) startMCPStream(r *http.Request, app App, streaming bool) func() {
	method := mcpTelemetryMethod(r)
	if !streaming || method == "" || h.metrics == nil || app.ID == "" || isRouteProbe(r.Context()) {
		return func() {}
	}
	gauge := h.metrics.mcpActive.WithLabelValues(app.ID, method)
	gauge.Inc()
	requestID := requestIDFrom(r)
	requestTraceID := traceIDForTelemetry(r.Context())
	receivedAt := startTime(r)
	return func() {
		gauge.Dec()
		if failure := recover(); failure != nil {
			outcome := "upstream_error"
			if err, ok := failure.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				outcome = "interrupted_stream"
			}
			elapsed := time.Since(receivedAt)
			h.metrics.mcpRequests.WithLabelValues(app.ID, method, outcome).Inc()
			h.metrics.mcpDuration.WithLabelValues(app.ID, method).Observe(elapsed.Seconds())
			if h.log != nil {
				h.log.Info("MCP forwarding aborted", "app_id", app.ID, "method", method, "outcome", outcome, "request_id", logsanitize.Field(requestID), "trace_id", requestTraceID)
			}
			panic(failure)
		}
	}
}
