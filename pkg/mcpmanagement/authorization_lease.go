package mcpmanagement

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authorizationLeaseExpiresKey struct{}

// Caller holds r.mu. Access expiry remains specific to the presented token.
func (r *remoteServer) accessLeaseLocked(key string) (*oauthGrant, time.Time, bool) {
	grant := r.access[key]
	if grant == nil || !grant.approved || grant.client == nil || grant.revoked {
		return nil, time.Time{}, false
	}
	expiry := grant.accessExpires
	if grant.expires.Before(expiry) {
		expiry = grant.expires
	}
	if !grant.credentialExpires.IsZero() && grant.credentialExpires.Before(expiry) {
		expiry = grant.credentialExpires
	}
	return grant, expiry, time.Now().Before(expiry)
}

func registerAuthorizationLease(s *mcp.Server) {
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			expiry, leased := ctx.Value(authorizationLeaseExpiresKey{}).(time.Time)
			if !leased {
				return next(ctx, method, req)
			}
			// Legacy HTTP peers detach request cancellation. Restore the authorization
			// deadline explicitly from retained context values for every SDK RPC.
			ctx, cancel := context.WithDeadline(ctx, expiry)
			defer cancel()
			return next(ctx, method, req)
		}
	})
}
