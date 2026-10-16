package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Telemetry aggregates bridge operations without account, grant, selector or URI
// labels. It owns a private registry; no default/global collectors are installed.
type Telemetry struct {
	registry       *prometheus.Registry
	requests       *prometheus.CounterVec
	duration       *prometheus.HistogramVec
	active         *prometheus.GaugeVec
	updates        prometheus.Counter
	consumerReads  *prometheus.CounterVec
	consumerLag    *prometheus.HistogramVec
	consumerAckAge prometheus.Histogram
	rejections     *prometheus.CounterVec
}

// NewTelemetry creates a process-level aggregate registry for management MCP.
func NewTelemetry() *Telemetry {
	t := &Telemetry{registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gregale_management_mcp_requests_total", Help: "Completed bridge requests and subscription snapshots by fixed operation, registered tool and outcome."}, []string{"operation", "tool", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gregale_management_mcp_request_duration_seconds", Help: "Bridge handler duration including admission, validation and customer API reads.", Buckets: []float64{.005, .025, .1, .5, 1, 5, 15, 30, 60, 120}}, []string{"operation", "tool"}),
		active:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gregale_management_mcp_active", Help: "Process aggregate reserved bridge capacity by fixed kind."}, []string{"kind"}),
		updates:  prometheus.NewCounter(prometheus.CounterOpts{Name: "gregale_management_mcp_resource_updates_total", Help: "URI invalidations successfully sent to individual subscription recipients."})}
	t.consumerReads = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gregale_management_mcp_consumer_reads_total", Help: "Successful consumer get observations by fixed status; repeated reads count again."}, []string{"status"})
	t.consumerLag = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gregale_management_mcp_consumer_lag_events", Help: "Read-time consumer event counts by retained or expired kind; sampled on successful initialized gets, not a journal-wide backlog.", Buckets: prometheus.ExponentialBuckets(1, 10, 16)}, []string{"kind"})
	t.consumerAckAge = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "gregale_management_mcp_consumer_ack_age_seconds", Help: "Time since last progress-changing acknowledgment sampled on successful gets; excludes uninitialized and future-dated acknowledgments. Not an expiry estimate.", Buckets: prometheus.ExponentialBuckets(1, 10, 9)})
	t.rejections = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gregale_management_mcp_admission_rejections_total", Help: "Requests rejected because their fixed bridge admission pool was full, by kind."}, []string{"kind"})
	t.registry.MustRegister(t.requests, t.duration, t.active, t.updates, t.consumerReads, t.consumerLag, t.consumerAckAge, t.rejections)
	for _, kind := range []string{"short_requests", "rollout_watches", "activity_watches", "listen_streams", "resource_slots"} {
		t.active.WithLabelValues(kind).Set(0)
	}
	for _, class := range []admissionClass{admissionShort, admissionRollout, admissionActivity} {
		t.rejections.WithLabelValues(string(class)).Add(0)
	}
	return t
}

// Handler exposes only this bridge's aggregate Prometheus registry. Embedders
// provide their own private listener or access controls.
func (t *Telemetry) Handler() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{})
}

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

func (t *Telemetry) observeConsumer(status string, lag *activityConsumerLag) {
	if t == nil || lag == nil {
		return
	}
	// Closed labels only, even if a future caller supplies an unexpected status.
	switch status {
	case "uninitialized", "ready", "cursor_expired":
	default:
		return
	}
	t.consumerReads.WithLabelValues(status).Inc()
	if lag.RetainedPendingEvents != nil {
		t.consumerLag.WithLabelValues("retained").Observe(float64(*lag.RetainedPendingEvents))
	}
	if lag.ExpiredUnacknowledgedEvents != nil {
		t.consumerLag.WithLabelValues("expired").Observe(float64(*lag.ExpiredUnacknowledgedEvents))
	}
	if lag.SecondsSinceAck != nil {
		t.consumerAckAge.Observe(*lag.SecondsSinceAck)
	}
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
	case "checkpoint_conflict", "consumer_generation_conflict", "consumer_capacity", "durable_state_required", "durable_state_unavailable", "cursor_expired", "recovery_restart_required":
		return code
	case "invalid_state":
		return "durable_state_unavailable"
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
