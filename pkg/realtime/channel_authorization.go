package realtime

// adr: 1385

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Share the existing SSE positive-decision lifetime with retained WebSockets.
// Durable access denies and tickets still use their separate one-second checks.
const channelReauthorizeInterval = 30 * time.Second

type channelVerdict uint8

const (
	channelAuthorizationAllowed channelVerdict = iota
	channelAuthorizationDenied
	channelAuthorizationUnavailable
)

// One record belongs to one bounded subscription, never to a caller-selected
// unbounded cache key. Its lock coalesces the history pump and outbound writer.
type channelAuthorization struct {
	mu        sync.Mutex
	checkedAt time.Time
	endpoint  *Endpoint
	denied    bool
	revoke    sync.Once
}

func (a *channelAuthorization) check(ctx context.Context, c *connection, channel string) channelVerdict {
	if ctx.Err() != nil || c.m.closed.Load() || c.state.revoked.Load() {
		return channelAuthorizationUnavailable
	}
	if !c.identityAllows(ctx) {
		return channelAuthorizationUnavailable
	}
	if c.ticketToken != "" {
		if c.accessAllows(ctx, channel) {
			return channelAuthorizationAllowed
		}
		return channelAuthorizationDenied
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx.Err() != nil {
		return channelAuthorizationUnavailable
	}
	select {
	case <-c.done:
		return channelAuthorizationUnavailable
	default:
	}
	current := c.state.config.Load()
	if current == nil || current.ClientAuth.Mode != AuthModeOIDCJWT || a.denied {
		return channelAuthorizationDenied
	}
	now := time.Now()
	if current == a.endpoint && !a.checkedAt.IsZero() && now.Sub(a.checkedAt) < channelReauthorizeInterval {
		return channelAuthorizationAllowed
	}
	allowed, err := c.m.checkResumeChannel(ctx, c, channel, current)
	if err != nil || ctx.Err() != nil || c.state.revoked.Load() || c.state.config.Load() != current || time.Since(now) >= channelReauthorizeInterval {
		return channelAuthorizationUnavailable
	}
	if !c.identityAllows(ctx) {
		return channelAuthorizationUnavailable
	}
	if !allowed {
		a.denied = true
		return channelAuthorizationDenied
	}
	a.checkedAt, a.endpoint = now, current
	return channelAuthorizationAllowed
}

func (m *Manager) admitOutboundResumeChannel(ctx context.Context, c *connection, channel string) bool {
	c.mu.RLock()
	subscription := c.resumeSubs[channel]
	c.mu.RUnlock()
	return subscription != nil && m.admitResumeChannel(ctx, c, channel, subscription)
}

func (m *Manager) admitResumeCommand(ctx context.Context, c *connection, frame resumeClientFrame) bool {
	if frame.Type == "subscribe" || frame.Type == "unsubscribe" || frame.Channel == "" {
		return true
	}
	c.mu.RLock()
	subscription := c.resumeSubs[frame.Channel]
	c.mu.RUnlock()
	// Commands without an active subscription retain their own validation,
	// including the fresh callback performed by a durable cursor reset.
	return subscription == nil || m.admitResumeChannel(ctx, c, frame.Channel, subscription)
}

func (m *Manager) admitResumeChannel(ctx context.Context, c *connection, channel string, subscription *resumeSubscription) bool {
	if ctx.Err() != nil {
		return false
	}
	c.mu.RLock()
	active := c.resumeSubs[channel] == subscription
	c.mu.RUnlock()
	if !active {
		return false
	}
	verdict := subscription.authorization.check(ctx, c, channel)
	// A slow check must not act on a replacement subscription.
	c.mu.RLock()
	active = c.resumeSubs[channel] == subscription
	c.mu.RUnlock()
	if !active || ctx.Err() != nil {
		return false
	}
	switch verdict {
	case channelAuthorizationAllowed:
		return true
	case channelAuthorizationDenied:
		m.revokeResumeChannel(c, channel, subscription)
	case channelAuthorizationUnavailable:
		_ = c.close(websocket.CloseTryAgainLater, "channel authorization unavailable")
	}
	return false
}

// Revocation belongs to the manager's lifecycle: subscription cleanup cancels
// its worker, but departure and control frames must still be delivered.
func (m *Manager) revokeResumeChannel(c *connection, channel string, subscription *resumeSubscription) {
	subscription.authorization.revoke.Do(func() {
		m.removeResumeSubscription(m.ctx, c, channel, subscription)
		m.queueResumeControl(m.ctx, c, resumeServerFrame{Type: "error", Channel: channel, Code: "access_revoked"})
	})
}
