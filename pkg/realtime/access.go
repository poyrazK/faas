package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const realtimeAccessRefreshInterval = time.Second
const realtimeAccessReadTimeout = 2 * time.Second

type ManagedRealtimeAccessReader interface {
	ReadRealtimeAccess(context.Context, string, string) (state.ManagedRealtimeAccess, error)
}

// Call only after the exact token has passed JWTAuthorizer signature and policy
// verification. Parsing here extracts the verified iat; it is not authentication.
func verifiedTokenIssuedAt(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("realtime: invalid verified token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	var claims struct {
		IssuedAt int64 `json:"iat"`
	}
	if err = json.Unmarshal(raw, &claims); err != nil || claims.IssuedAt <= 0 || claims.IssuedAt > time.Now().Unix() {
		return time.Time{}, errors.New("realtime: invalid token issued-at")
	}
	return time.Unix(claims.IssuedAt, 0).UTC(), nil
}

// verifiedTokenExpiry reads the exp claim of a token the authorizer already
// verified. A zero time means the token carries none.
func verifiedTokenExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Expiry int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Expiry <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Expiry, 0).UTC()
}

func sessionRevoked(access state.ManagedRealtimeAccess, issued time.Time) bool {
	return access.SessionsRevokedBefore != nil && (issued.IsZero() || !issued.After(*access.SessionsRevokedBefore))
}
func channelDenied(access state.ManagedRealtimeAccess, channel string) bool {
	if channel == "" {
		return false
	}
	parent, _, err := api.ParseRealtimeActivityScope(channel)
	if err != nil {
		return true
	}
	for _, denied := range access.DeniedChannels {
		if denied == channel || (parent != "" && denied == parent) {
			return true
		}
	}
	return false
}

func (c *connection) accessEnabled() bool {
	return c.ticketToken == "" && c.endpointConfig().accessOn(c.m.cfg) && c.endpoint.ClientAuth.Mode == AuthModeOIDCJWT
}

func (c *connection) refreshAccess(ctx context.Context) (state.ManagedRealtimeAccess, error) {
	c.accessMu.Lock()
	defer c.accessMu.Unlock()
	if time.Since(c.accessChecked) < realtimeAccessRefreshInterval {
		return c.access, nil
	}
	snapshot, err := c.m.cachedAccess(ctx, c.info.EndpointID, c.info.Principal)
	if err != nil {
		c.accessChecked = time.Time{}
		return c.access, err
	}
	c.access = snapshot
	c.accessChecked = time.Now()
	return snapshot, nil
}
func (c *connection) accessAllows(ctx context.Context, channel string) bool {
	if c.ticketToken != "" {
		return c.ticketAllows(ctx, channel)
	}
	if !c.accessEnabled() {
		return true
	}
	snapshot, err := c.refreshAccess(ctx)
	if !c.closeOnAccessFailure(snapshot, err) {
		return false
	}
	return !channelDenied(snapshot, channel)
}

func (c *connection) monitorAccess() {
	ticker := time.NewTicker(realtimeAccessRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-c.m.ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := c.refreshAccess(c.m.ctx)
			if !c.closeOnAccessFailure(snapshot, err) {
				return
			}
			c.mu.RLock()
			channels := make([]string, 0, len(c.channels))
			for channel := range c.channels {
				channels = append(channels, channel)
			}
			c.mu.RUnlock()
			for _, channel := range channels {
				if !channelDenied(snapshot, channel) {
					continue
				}
				if c.v2 {
					c.mu.RLock()
					subscription := c.resumeSubs[channel]
					c.mu.RUnlock()
					if subscription != nil {
						subscription.cancel()
						c.m.removeResumeSubscription(c.m.ctx, c, channel, subscription)
					}
					c.m.queueResumeControl(c.m.ctx, c, resumeServerFrame{Type: "error", Code: "access_revoked", Channel: channel})
				} else {
					_ = c.m.Unsubscribe(c.info.ID, channel)
				}
			}
		}
	}
}

// closeOnAccessFailure closes the connection and reports false when its
// session was revoked (1008: reconnect with new credentials) or its access
// could not be checked for realtimeAccessStaleMax (1013: try again later).
func (c *connection) closeOnAccessFailure(snapshot state.ManagedRealtimeAccess, err error) bool {
	if err != nil {
		_ = c.close(websocket.CloseTryAgainLater, "session access unavailable")
		return false
	}
	if sessionRevoked(snapshot, c.issuedAt) {
		_ = c.close(websocket.ClosePolicyViolation, "session access revoked")
		return false
	}
	return true
}
