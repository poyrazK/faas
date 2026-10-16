package mcpmanagement

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Local bridge concurrency bounds, independent of platform/billing quotas.
const maxShortRequests = api.MCPShortRequestsPerConnection
const maxRolloutWatches = api.MCPRolloutWatchesPerConnection
const maxActivityWatches = api.MCPActivityWatchesPerConnection

type admissionClass string

const (
	admissionShort    admissionClass = "short_requests"
	admissionRollout  admissionClass = "rollout_watches"
	admissionActivity admissionClass = "activity_watches"
)

type requestAdmission struct {
	telemetry *Telemetry
	short     chan struct{}
	watches   chan struct{}
	activity  chan struct{}
}

func newRequestAdmission(telemetry *Telemetry) *requestAdmission {
	return &requestAdmission{telemetry: telemetry, short: make(chan struct{}, maxShortRequests), watches: make(chan struct{}, maxRolloutWatches), activity: make(chan struct{}, maxActivityWatches)}
}

func (a *requestAdmission) acquire(ctx context.Context, class admissionClass) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var slots chan struct{}
	switch class {
	case admissionShort:
		slots = a.short
	case admissionRollout:
		slots = a.watches
	case admissionActivity:
		slots = a.activity
	default:
		return nil, invalid("invalid_argument", "unknown MCP admission class")
	}
	kind := string(class)
	select {
	case slots <- struct{}{}:
		a.telemetry.change(kind, 1)
		return func() { a.telemetry.change(kind, -1); <-slots }, nil
	default:
		if a.telemetry != nil {
			a.telemetry.rejections.WithLabelValues(kind).Inc()
		}
		retry := int64(1)
		return nil, &api.APIError{Problem: api.Problem{Status: 429, Code: "mcp_busy", Title: "MCP connection busy", Detail: "This connection has reached its concurrent request limit; retry after capacity is released", Hint: "Retry after at least one second with jitter. For mutations, reuse the same plan ID.", RetryAfterSeconds: &retry}}
	}
}

func toolAdmissionClass(name string) admissionClass {
	switch name {
	case "gregale_watch_rollout":
		return admissionRollout
	case "gregale_watch_environment_cache_schedule_activity", watchActivityConsumerTool:
		return admissionActivity
	default:
		return admissionShort
	}
}

func registerRequestAdmission(s *mcp.Server, m *management) {
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			class := admissionShort
			switch method {
			case "tools/call":
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
					class = toolAdmissionClass(p.Name)
				}
			case "resources/read", "prompts/get", "completion/complete":
			default:
				return next(ctx, method, req)
			}
			release, err := m.admission.acquire(ctx, class)
			if err != nil {
				if method == "tools/call" {
					return m.toolError(err), nil
				}
				return nil, m.contextError(err)
			}
			defer release()
			return next(ctx, method, req)
		}
	})
}

func requestBusy(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == "mcp_busy"
}
