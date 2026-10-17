package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Live queries (ADR-1384) ride v2 connections as live.* frames. realtimed
// only carries them: Config.Live (pkg/livequery/liveproxy in cmd/realtimed)
// sends each client's frames to the hub serving the sealed query's database
// and queues the hub's frames back. Live frames are not channel traffic, so
// channel access policies and read-only tickets do not apply to them; the
// seal is the capability.

// LiveQueries carries a connection's live frames.
type LiveQueries interface {
	// Attach starts a connection's live session. send queues a frame to the
	// client and may be called from any goroutine.
	Attach(send func(frame []byte) error) LiveSession
}

// LiveSession is one connection's live queries.
type LiveSession interface {
	Receive(ctx context.Context, frame []byte)
	Close()
}

// liveFrameType returns the type of a live.* frame, or "".
func liveFrameType(data []byte) (string, string) {
	var head struct {
		Type         string `json:"type"`
		Subscription string `json:"subscription"`
	}
	if json.Unmarshal(data, &head) != nil || !strings.HasPrefix(head.Type, "live.") {
		return "", ""
	}
	return head.Type, head.Subscription
}

// handleLive passes a live frame to the connection's live session.
func (m *Manager) handleLive(ctx context.Context, c *connection, data []byte, subscription string) {
	c.mu.Lock()
	c.info.LastSeen = time.Now().UTC()
	c.mu.Unlock()
	if m.cfg.Live == nil {
		frame, _ := json.Marshal(map[string]any{
			"type": "live.error", "subscription": subscription, "code": "unavailable",
			"message": "live queries are not enabled on this endpoint", "closed": true,
		})
		_ = m.queueLive(ctx, c, frame)
		return
	}
	// The session's frames arrive from the hub, outside this frame's context.
	c.liveOnce.Do(func() { //nolint:contextcheck // queued frames outlive the client frame that started the session
		c.live = m.cfg.Live.Attach(func(frame []byte) error { return m.queueLive(context.Background(), c, frame) })
	})
	c.live.Receive(ctx, data)
}

// queueLive queues a live frame. Live frames count against the connection's
// outbound budget like any other; a client too slow for them is closed.
func (m *Manager) queueLive(ctx context.Context, c *connection, frame []byte) error {
	err := m.queueOutbound(ctx, c, Message{Data: frame})
	if errors.Is(err, ErrOutboundQueueFull) {
		_ = c.close(websocket.CloseTryAgainLater, resumeOutputQueueFullReason)
	}
	if err == nil {
		m.sentMessages.Add(1)
		m.sentBytes.Add(uint64(len(frame)))
	}
	return err
}

// closeLive ends the connection's live session.
func (c *connection) closeLive() {
	c.liveOnce.Do(func() {}) // no session may start after this
	if c.live != nil {
		c.live.Close()
	}
}
