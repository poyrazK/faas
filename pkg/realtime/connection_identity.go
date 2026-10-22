package realtime

// adr: 1332

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// One immutable token and verified identity belong to a retained connection.
// The mutex coalesces checks from channels, commands, the writer and idle timer.
type connectionIdentity struct {
	mu        sync.Mutex
	token     string
	identity  JWTIdentity
	request   *http.Request
	endpoint  *Endpoint
	checkedAt time.Time
}

func (a *connectionIdentity) check(ctx context.Context, c *connection) channelVerdict {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx.Err() != nil || c.m.closed.Load() || c.state.revoked.Load() {
		return channelAuthorizationUnavailable
	}
	select {
	case <-c.done:
		return channelAuthorizationUnavailable
	default:
	}
	current := c.state.config.Load()
	if current == nil || current.ClientAuth.Mode != AuthModeOIDCJWT {
		return channelAuthorizationDenied
	}
	now := time.Now()
	if !a.identity.ExpiresAt.IsZero() && !now.Before(a.identity.ExpiresAt) {
		return channelAuthorizationDenied
	}
	if current == a.endpoint && now.Sub(a.checkedAt) < channelReauthorizeInterval {
		return channelAuthorizationAllowed
	}
	identity, err := c.m.authorizeIdentity(ctx, a.token, current.ClientAuth)
	if err != nil {
		return channelAuthorizationUnavailable
	}
	if identity.Principal != a.identity.Principal || identity.SessionID != a.identity.SessionID {
		return channelAuthorizationDenied
	}
	principal := identity.Principal
	if current.Authorize != nil {
		if a.request == nil {
			return channelAuthorizationUnavailable
		}
		authCtx, cancel := context.WithTimeout(ctx, c.m.cfg.CallbackTimeout)
		principal, err = current.Authorize(authCtx, a.request)
		if err == nil {
			err = authCtx.Err()
		}
		cancel()
		if err != nil {
			return channelAuthorizationUnavailable
		}
	}
	if ctx.Err() != nil || c.state.revoked.Load() || c.state.config.Load() != current || time.Since(now) >= channelReauthorizeInterval {
		return channelAuthorizationUnavailable
	}
	if principal != c.info.Principal {
		return channelAuthorizationDenied
	}
	if !a.identity.ExpiresAt.IsZero() && !time.Now().Before(a.identity.ExpiresAt) {
		return channelAuthorizationDenied
	}
	a.endpoint, a.checkedAt = current, now
	return channelAuthorizationAllowed
}

func (c *connection) identityAllows(ctx context.Context) bool {
	if c.identity == nil {
		return true
	}
	verdict := c.identity.check(ctx, c)
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
	}
	switch verdict {
	case channelAuthorizationAllowed:
		return true
	case channelAuthorizationDenied:
		_ = c.close(websocket.ClosePolicyViolation, "session identity invalid or expired")
	case channelAuthorizationUnavailable:
		_ = c.close(websocket.CloseTryAgainLater, "identity authorization unavailable")
	}
	return false
}

func (c *connection) monitorIdentity() {
	ticker := time.NewTicker(channelReauthorizeInterval)
	defer ticker.Stop()
	var expires <-chan time.Time
	if expiry := c.identity.identity.ExpiresAt; !expiry.IsZero() {
		timer := time.NewTimer(time.Until(expiry))
		defer timer.Stop()
		expires = timer.C
	}
	for {
		select {
		case <-ticker.C:
		case <-expires:
		case <-c.done:
			return
		case <-c.m.ctx.Done():
			return
		}
		if !c.identityAllows(c.m.ctx) {
			return
		}
	}
}
