package gateway

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type devBridgeScopeKey struct{}
type devBridgeScope struct{ account, environment, app string }

// WithDevBridgeScope is set only by the daemon's durable session verifier.
func WithDevBridgeScope(ctx context.Context, account, environment, app string) context.Context {
	return context.WithValue(ctx, devBridgeScopeKey{}, devBridgeScope{account, environment, app})
}

func hasDevBridgeScope(ctx context.Context) bool {
	scope, ok := ctx.Value(devBridgeScopeKey{}).(devBridgeScope)
	return ok && scope.account != "" && scope.environment != "" && scope.app != ""
}

func DevBridgeAllowsPrivateEnvironment(ctx context.Context, account, environment, app string) bool {
	scope, ok := ctx.Value(devBridgeScopeKey{}).(devBridgeScope)
	equal := func(a, b string) bool {
		left, e1 := uuid.Parse(a)
		right, e2 := uuid.Parse(b)
		return e1 == nil && e2 == nil && left == right
	}
	return ok && equal(scope.account, account) && equal(scope.environment, environment) && equal(scope.app, app)
}

func (h *Handler) WithDevBridge(authorize func(*http.Request) *api.Problem, forward func(http.ResponseWriter, *http.Request, App) bool) *Handler {
	h.devBridgeAuthorize = authorize
	h.devBridgeForward = forward
	return h
}
