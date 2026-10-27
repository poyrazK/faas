package realtime

import (
	"context"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/state"
)

type SubscriptionTicketReader interface {
	ReadSubscriptionTicket(context.Context, string, string) (state.RealtimeSubscriptionTicket, error)
}

func (m *Manager) readTicket(ctx context.Context, ep, token string) (state.RealtimeSubscriptionTicket, error) {
	readCtx, cancel := context.WithTimeout(ctx, realtimeAccessReadTimeout)
	defer cancel()
	return m.cfg.TicketReader.ReadSubscriptionTicket(readCtx, ep, token)
}
func (c *connection) ticketAllows(ctx context.Context, channel string) bool {
	c.accessMu.Lock()
	defer c.accessMu.Unlock()
	if !c.ticketClaims.ExpiresAt.IsZero() && !time.Now().Before(c.ticketClaims.ExpiresAt) {
		_ = c.close(websocket.CloseNormalClosure, "ticket expired")
		return false
	}
	if time.Since(c.ticketChecked) >= realtimeAccessRefreshInterval {
		claims, err := c.m.readTicket(ctx, c.info.EndpointID, c.ticketToken)
		if err != nil || claims.Principal != c.info.Principal {
			c.ticketChecked = time.Time{}
			_ = c.close(websocket.ClosePolicyViolation, "ticket expired, revoked or unavailable")
			return false
		}
		c.ticketClaims = claims
		c.ticketChecked = time.Now()
	}
	if !time.Now().Before(c.ticketClaims.ExpiresAt) {
		_ = c.close(websocket.CloseNormalClosure, "ticket expired")
		return false
	}
	if channel == "" {
		return true
	}
	for _, allowed := range c.ticketClaims.Channels {
		if allowed == channel {
			return true
		}
	}
	return false
}
func (c *connection) monitorTicket() {
	timer := time.NewTicker(realtimeAccessRefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-c.m.ctx.Done():
			return
		case <-timer.C:
			if !c.ticketAllows(c.m.ctx, "") {
				return
			}
			c.mu.RLock()
			subs := make(map[string]*resumeSubscription, len(c.resumeSubs))
			for ch, sub := range c.resumeSubs {
				subs[ch] = sub
			}
			c.mu.RUnlock()
			for ch, sub := range subs {
				if !c.ticketAllows(c.m.ctx, ch) {
					m := c.m
					m.removeResumeSubscription(c.m.ctx, c, ch, sub)
					m.queueResumeControl(c.m.ctx, c, resumeServerFrame{Type: "error", Code: "access_revoked", Channel: ch})
				}
			}
		}
	}
}
func isTicket(token string) bool { return strings.HasPrefix(token, "grt_") }

func (m *Manager) startTicketSubscription(ctx context.Context, c *connection, frame resumeClientFrame, page state.ManagedRealtimeChannelHistory) bool {
	subCtx, cancel := context.WithCancel(ctx)
	sub := &resumeSubscription{presence: channelAuthorization{presence: true}, filter: frame.Filter, cancel: cancel, wake: make(chan struct{}, 1), lastSent: frame.After, lastAck: frame.After}
	c.mu.Lock()
	c.resumeSubs[frame.Channel] = sub
	c.addChannelLocked(frame.Channel)
	c.mu.Unlock()
	if err := m.queueResume(ctx, c, resumeServerFrame{Type: "subscribed", Channel: frame.Channel, Sequence: frame.After, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}); err != nil {
		m.removeResumeSubscription(ctx, c, frame.Channel, sub)
		return false
	}
	m.watchSubscriptionOccupancy(ctx, c, frame)
	go m.pumpResumeSubscription(subCtx, c, frame.Channel, sub, page)
	return true
}
