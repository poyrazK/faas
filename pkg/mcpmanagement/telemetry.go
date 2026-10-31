package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Telemetry aggregates bridge operations without account, grant, selector or URI
// labels. It owns a private registry; no default/global collectors are installed.
type Telemetry struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	active   *prometheus.GaugeVec
	updates  prometheus.Counter
	consent  *prometheus.CounterVec
	// connections reads a remote bridge's state at scrape time.
	connections atomic.Pointer[func() connectionStats]
}

// NewTelemetry creates a process-level aggregate registry for management MCP.
func NewTelemetry() *Telemetry {
	t := &Telemetry{registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gregale_management_mcp_requests_total", Help: "Completed bridge requests and subscription snapshots by fixed operation, registered tool and outcome."}, []string{"operation", "tool", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gregale_management_mcp_request_duration_seconds", Help: "Bridge handler duration including admission, validation and customer API reads.", Buckets: []float64{.005, .025, .1, .5, 1, 5, 15, 30, 60, 120}}, []string{"operation", "tool"}),
		active:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gregale_management_mcp_active", Help: "Process aggregate reserved bridge capacity by fixed kind."}, []string{"kind"}),
		updates:  prometheus.NewCounter(prometheus.CounterOpts{Name: "gregale_management_mcp_resource_updates_total", Help: "URI invalidations successfully sent to individual subscription recipients."}),
		consent:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gregale_management_mcp_consent_total", Help: "Agent connection consent steps reached: started, sign_in_started, signed_in, approved, denied (ADR-20261028205920)."}, []string{"step"})}
	t.registry.MustRegister(t.requests, t.duration, t.active, t.updates, t.consent, connectionCollector{t})
	for _, kind := range []string{"short_requests", "rollout_watches", "listen_streams", "resource_slots"} {
		t.active.WithLabelValues(kind).Set(0)
	}
	for _, step := range consentSteps {
		t.consent.WithLabelValues(step).Add(0)
	}
	return t
}

// consentSteps are the funnel an agent connection passes through.
var consentSteps = []string{"started", "sign_in_started", "signed_in", "approved", "denied"}

// consentStep counts one flow reaching step.
func (t *Telemetry) consentStep(step string) {
	if t != nil {
		t.consent.WithLabelValues(step).Inc()
	}
}

// Handler exposes only this bridge's aggregate Prometheus registry. Embedders
// provide their own private listener or access controls.
func (t *Telemetry) Handler() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{})
}

// Gatherer exposes the same registry to an embedder that serves it beside
// its own process metrics.
func (t *Telemetry) Gatherer() prometheus.Gatherer { return t.registry }

func (t *Telemetry) change(kind string, amount float64) {
	if t != nil {
		t.active.WithLabelValues(kind).Add(amount)
	}
}
func (t *Telemetry) observe(operation, tool, outcome string, start time.Time) {
	if t == nil {
		return
	}
	t.requests.WithLabelValues(operation, tool, outcome).Inc()
	t.duration.WithLabelValues(operation, tool).Observe(time.Since(start).Seconds())
}

func telemetryOutcome(ctx context.Context, result mcp.Result, err error) string {
	if r, ok := result.(*mcp.CallToolResult); ok && r != nil && r.IsError {
		if p, ok := r.StructuredContent.(ToolError); ok {
			return telemetryCode(p.Code)
		}
		return "error"
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return "timeout"
		}
		var local *inputError
		if errors.As(err, &local) {
			return telemetryCode(local.code)
		}
		var upstream *api.APIError
		if errors.As(err, &upstream) {
			return telemetryCode(upstream.Problem.Code)
		}
		if ctx.Err() != nil {
			return telemetryOutcome(ctx, nil, ctx.Err())
		}
		return "error"
	}
	return "success"
}
func telemetryCode(code string) string {
	switch code {
	case "mcp_busy", "subscription_capacity":
		return "busy"
	case "cancelled":
		return "cancelled"
	case "timeout":
		return "timeout"
	default:
		return "error"
	}
}

func registerTelemetry(s *mcp.Server, m *management) {
	if m.opts.Telemetry == nil {
		return
	}
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/call", "resources/read", "prompts/get", "completion/complete", "subscriptions/listen":
			default:
				return next(ctx, method, req)
			}
			tool := "none"
			if method == "tools/call" {
				tool = "unknown"
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && m.toolNames[p.Name] {
					tool = p.Name
				}
			}
			start := time.Now()
			result, err := next(ctx, method, req)
			m.opts.Telemetry.observe(method, tool, telemetryOutcome(ctx, result, err), start)
			return result, err
		}
	})
}
