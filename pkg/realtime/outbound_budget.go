package realtime

import (
	"context"
	"slices"
	"time"

	"github.com/gorilla/websocket"
)

// Node outbound budget (ADR-1323). Every connection copies the payloads queued
// for it, so per-connection limits alone allow MaxConnections times
// OutboundQueueBytes of buffered data, far beyond realtimed's memory. The node
// budget bounds the sum: frames that would exceed it are refused like a full
// per-connection queue. Before that point a shedder disconnects the
// connections holding the largest backlogs, which are the slow consumers, so
// healthy connections keep receiving. Shed clients get close code 1013 and
// reconnect; resumable clients recover from retained history.
const (
	// Shedding starts once queued bytes exceed this share of the budget and
	// stops when they fall to outboundShedTargetPercent.
	outboundShedStartPercent  = 75
	outboundShedTargetPercent = 50
	// Only a connection holding at least 1/outboundShedMinBacklogDivisor of
	// its own queue limit (256 KiB by default) is shed; small backlogs are
	// ordinary bursts.
	outboundShedMinBacklogDivisor = 16
	// outboundShedCooldown spaces shedding passes so a node that stays near
	// the budget does not scan every connection on each queued frame.
	outboundShedCooldown = 100 * time.Millisecond

	outboundNodeBudgetReason = "node outbound budget exceeded"
)

// reserveNodeOutbound accounts n queued bytes against the node budget, or
// reports that they do not fit. Releasing bytes (n <= 0) always succeeds, and
// so does everything when the budget is disabled.
func (m *Manager) reserveNodeOutbound(n int64) bool {
	limit := m.cfg.OutboundNodeBytes
	if n <= 0 || limit <= 0 {
		m.outboundPendingBytes.Add(n)
		return true
	}
	for {
		current := m.outboundPendingBytes.Load()
		if current > limit-n {
			m.wakeOutboundShed()
			return false
		}
		if m.outboundPendingBytes.CompareAndSwap(current, current+n) {
			if current+n > limit*outboundShedStartPercent/100 {
				m.wakeOutboundShed()
			}
			return true
		}
	}
}

func (m *Manager) wakeOutboundShed() {
	select {
	case m.outboundShed <- struct{}{}:
	default:
	}
}

func (m *Manager) startOutboundShed() {
	if m.cfg.OutboundNodeBytes <= 0 {
		return
	}
	go m.runOutboundShed(m.ctx) //nolint:contextcheck // the Manager owns the shedder's lifecycle; Close cancels it.
}

func (m *Manager) runOutboundShed(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.outboundShed:
		}
		m.shedOutbound()
		select {
		case <-ctx.Done():
			return
		case <-time.After(outboundShedCooldown):
		}
	}
}

// shedOutbound disconnects the largest backlogs until queued bytes fall to the
// target share of the budget, or no connection holds a sheddable backlog.
func (m *Manager) shedOutbound() {
	limit := m.cfg.OutboundNodeBytes
	if m.outboundPendingBytes.Load() <= limit*outboundShedStartPercent/100 {
		return
	}
	target := limit * outboundShedTargetPercent / 100
	for _, victim := range m.outboundShedCandidates() {
		if m.outboundPendingBytes.Load() <= target {
			return
		}
		m.outboundShedDisconnects.Add(1)
		victim.shed(websocket.CloseTryAgainLater, outboundNodeBudgetReason)
	}
}

// outboundShedCandidates lists open connections with a sheddable backlog,
// largest first. The snapshot is taken without holding m.mu while reading
// backlogs, so queueing under m.mu cannot deadlock against it.
func (m *Manager) outboundShedCandidates() []*connection {
	m.mu.RLock()
	connections := make([]*connection, 0, len(m.conns))
	for _, c := range m.conns {
		connections = append(connections, c)
	}
	m.mu.RUnlock()
	minimum := m.cfg.OutboundQueueBytes / outboundShedMinBacklogDivisor
	type candidate struct {
		conn    *connection
		backlog int64
	}
	candidates := make([]candidate, 0, 8)
	for _, c := range connections {
		c.outboundMu.Lock()
		backlog, closed := c.outboundBytes, c.outboundClosed
		c.outboundMu.Unlock()
		if !closed && backlog >= minimum {
			candidates = append(candidates, candidate{conn: c, backlog: backlog})
		}
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		switch {
		case a.backlog > b.backlog:
			return -1
		case a.backlog < b.backlog:
			return 1
		}
		return 0
	})
	out := make([]*connection, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate.conn
	}
	return out
}

// shed releases a connection's queued bytes at once and sends the close frame
// in the background: a slow consumer's frame may wait behind a stalled write
// for up to WriteWait, which must not hold up the shedder.
func (c *connection) shed(code int, reason string) {
	c.closeOne.Do(func() {
		c.recordClose(code, reason)
		c.closeOutboundQueue()
		go func() {
			_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(c.m.cfg.WriteWait))
			_ = c.ws.Close()
		}()
	})
}
