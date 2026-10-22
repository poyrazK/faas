package mcpmanagement

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Local bridge concurrency bounds, independent of platform/billing quotas.
const maxShortRequests = 8
const maxRolloutWatches = 2

type requestAdmission struct {
	telemetry *Telemetry
	short     chan struct{}
	watches   chan struct{}
}

func newRequestAdmission(telemetry *Telemetry) *requestAdmission {
	return &requestAdmission{telemetry: telemetry, short: make(chan struct{}, maxShortRequests), watches: make(chan struct{}, maxRolloutWatches)}
}

func (a *requestAdmission) acquire(ctx context.Context, watch bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind := "short_requests"
	slots := a.short
	if watch {
		kind = "rollout_watches"
		slots = a.watches
	}
	select {
	case slots <- struct{}{}:
		a.telemetry.change(kind, 1)
		return func() { a.telemetry.change(kind, -1); <-slots }, nil
	default:
		retry := int64(1)
		return nil, &api.APIError{Problem: api.Problem{Status: 429, Code: "mcp_busy", Title: "MCP connection busy", Detail: "This connection has reached its concurrent request limit; retry after capacity is released", Hint: "Retry after at least one second with jitter. For mutations, reuse the same plan ID.", RetryAfterSeconds: &retry}}
	}
}

func registerRequestAdmission(s *mcp.Server, m *management) {
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			watch := false
			switch method {
			case "tools/call":
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
					watch = p.Name == "gregale_watch_rollout"
				}
			case "resources/read", "prompts/get", "completion/complete":
			default:
				return next(ctx, method, req)
			}
			release, err := m.admission.acquire(ctx, watch)
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
