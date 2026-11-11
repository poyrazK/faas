package realtime

import (
	"sync"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
)

// Client frames that cost a control-plane call or a live hub subscription are
// bounded per node and per endpoint (ADR-1323, amendment of 2026-11-03).
// Signals, presence updates and read receipts each reach the other nodes
// through apid; past the budget they are refused with their rate-limited
// code before anything is delivered, so every node sees the same frames.

// relayBudget is one endpoint's share of the node's ephemeral relays.
type relayBudget struct {
	once    sync.Once
	limiter *rate.Limiter
}

func (b *relayBudget) allow() bool {
	b.once.Do(func() {
		b.limiter = rate.NewLimiter(rate.Limit(api.RealtimeEndpointEphemeralRelaysPerSecond), api.RealtimeEndpointEphemeralRelayBurst)
	})
	return b.limiter.Allow()
}

// allowEphemeralRelay takes one relay from the connection's endpoint and the
// node. A node without a fleet client relays nothing, so it spends nothing.
func (m *Manager) allowEphemeralRelay(c *connection) bool {
	if m.cfg.FleetClient == nil {
		return true
	}
	if c.state != nil && !c.state.relays.allow() {
		m.ephemeralRelaysThrottled.Add(1)
		return false
	}
	if m.ephemeralRelays != nil && !m.ephemeralRelays.Allow() {
		m.ephemeralRelaysThrottled.Add(1)
		return false
	}
	return true
}

// allowLiveFrame takes one live frame from the connection and the node.
func (m *Manager) allowLiveFrame(c *connection) bool {
	if c.liveFrames == nil {
		c.liveFrames = rate.NewLimiter(rate.Limit(api.RealtimeLiveFramesPerSecond), api.RealtimeLiveFrameBurst)
	}
	if !c.liveFrames.Allow() || (m.liveFrames != nil && !m.liveFrames.Allow()) {
		m.liveFramesThrottled.Add(1)
		return false
	}
	return true
}

// allowHandshake takes one new connection from the node's handshake rate.
func (m *Manager) allowHandshake() bool {
	if m.handshakes == nil || m.handshakes.Allow() {
		return true
	}
	m.handshakesThrottled.Add(1)
	return false
}
