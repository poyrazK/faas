package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	ResumeSubprotocol         = "gregale.realtime.v2"
	maxResumeSubscriptions    = api.RealtimeResumeSubscriptionsPerNode
	maxResumePerConnection    = api.RealtimeResumeSubscriptionsPerConnection
	resumeHistoryPageSize     = state.ManagedRealtimeHistoryMaxRead
	resumeHistoryReadTimeout  = 5 * time.Second
	maxResumeServerFrameBytes = api.RealtimeResumeServerFrameMaxBytes
)

// ManagedRealtimeHistoryReader is the private apid RPC seam. A nil reader
// prevents v2 admission; realtimed never connects directly to PostgreSQL.
type ManagedRealtimeHistoryReader interface {
	ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error)
}

// v2 cursors are client-held. Ack confirms a sequence actually sent on this
// connection; clients persist the acknowledged cursor before reconnecting.
// It can be replayed after a lost ack, so consumers deduplicate by sequence.
type resumeClientFrame struct {
	Type     string `json:"type"`
	Channel  string `json:"channel"`
	After    int64  `json:"after,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
}

type resumeServerFrame struct {
	Type           string `json:"type"`
	Channel        string `json:"channel,omitempty"`
	Code           string `json:"code,omitempty"`
	Sequence       int64  `json:"sequence,omitempty"`
	OldestSequence int64  `json:"oldest_sequence,omitempty"`
	LatestSequence int64  `json:"latest_sequence,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	DataBase64     string `json:"data_base64,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
}

type resumeSubscription struct {
	cancel   context.CancelFunc
	lastSent int64 // guarded by connection.mu
	lastAck  int64 // guarded by connection.mu
}

func (m *Manager) runResumeConnection(ctx context.Context, c *connection) {
	for {
		kind, data, err := c.ws.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				c.recordClose(closeErr.Code, closeErr.Text)
			}
			return
		}
		if kind != websocket.TextMessage {
			_ = c.close(websocket.CloseUnsupportedData, "v2 requires JSON text frames")
			return
		}
		var frame resumeClientFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_frame"})
			continue
		}
		c.mu.Lock()
		c.info.LastSeen = time.Now().UTC()
		c.mu.Unlock()
		switch frame.Type {
		case "subscribe":
			m.resumeSubscribe(ctx, c, frame)
		case "ack":
			m.resumeAck(ctx, c, frame)
		case "unsubscribe":
			m.resumeUnsubscribe(ctx, c, frame.Channel)
		default:
			_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_frame"})
		}
	}
}

func (m *Manager) reserveResume() bool {
	for {
		current := m.resumeCount.Load()
		if current >= maxResumeSubscriptions {
			return false
		}
		if m.resumeCount.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (m *Manager) resumeSubscribe(ctx context.Context, c *connection, frame resumeClientFrame) {
	if !validChannel(frame.Channel) || frame.After < 0 || frame.Sequence != 0 {
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_subscription"})
		return
	}
	c.mu.RLock()
	_, exists := c.resumeSubs[frame.Channel]
	count := len(c.resumeSubs)
	c.mu.RUnlock()
	if exists || count >= maxResumePerConnection || !m.reserveResume() {
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "subscription_limit"})
		return
	}
	release := true
	defer func() {
		if release {
			m.resumeCount.Add(-1)
		}
	}()
	authorizer := m.hooks.(ChannelAuthorizer) // checked before WebSocket upgrade
	event := Event{
		ID: "evt_" + uuid.NewString(), Type: EventAuthorizeChannel,
		EndpointID: c.info.EndpointID, AppID: c.info.AppID, AccountID: c.info.AccountID,
		ConnectionID: c.info.ID, Principal: c.info.Principal, Channel: frame.Channel,
		Permission: "read", At: time.Now().UTC(), CallbackURL: c.endpoint.CallbackURL,
		CallbackAuthToken: c.currentCallbackAuthToken(),
	}
	authCtx, cancel := context.WithTimeout(ctx, m.cfg.CallbackTimeout)
	allowed, err := authorizer.AuthorizeChannel(authCtx, event)
	cancel()
	if err != nil || !allowed {
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_authorized"})
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	page, err := m.cfg.HistoryReader.ReadChannelHistory(readCtx, c.info.EndpointID, frame.Channel, frame.After, resumeHistoryPageSize)
	cancel()
	if err != nil {
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "history_read_failed"})
		return
	}
	if page.HistoryUnavailable {
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "resync_required", Channel: frame.Channel,
			OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
		return
	}
	subCtx, subCancel := context.WithCancel(m.ctx)
	subscription := &resumeSubscription{cancel: subCancel, lastSent: frame.After, lastAck: frame.After}
	c.mu.Lock()
	c.resumeSubs[frame.Channel] = subscription
	c.channels[frame.Channel] = struct{}{}
	c.mu.Unlock()
	if err := m.queueResume(ctx, c, resumeServerFrame{Type: "subscribed", Channel: frame.Channel,
		Sequence: frame.After, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}); err != nil {
		m.removeResumeSubscription(c, frame.Channel, subscription)
		return
	}
	release = false
	go m.pumpResumeSubscription(subCtx, c, frame.Channel, subscription, page)
}

func (m *Manager) pumpResumeSubscription(ctx context.Context, c *connection, channel string, subscription *resumeSubscription, page state.ManagedRealtimeChannelHistory) {
	defer m.resumeCount.Add(-1)
	defer m.removeResumeSubscription(c, channel, subscription)
	ticker := time.NewTicker(m.cfg.ResumePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if page.HistoryUnavailable {
			_ = m.queueResume(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
				OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
			return
		}
		for _, message := range page.Messages {
			c.mu.Lock()
			if c.resumeSubs[channel] != subscription || ctx.Err() != nil {
				c.mu.Unlock()
				return
			}
			lastSent := subscription.lastSent
			if message.Sequence != lastSent+1 {
				c.mu.Unlock()
				_ = m.queueResume(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
					OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
				return
			}
			if err := m.queueResume(ctx, c, resumeServerFrame{
				Type: "message", Channel: channel, Sequence: message.Sequence,
				MessageID:  uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.info.EndpointID+"\x00"+channel+"\x00"+strconv.FormatInt(message.Sequence, 10))).String(),
				DataBase64: base64.StdEncoding.EncodeToString(message.Data), Binary: message.Binary,
			}); err != nil {
				c.mu.Unlock()
				_ = c.close(websocket.CloseTryAgainLater, "resume output queue full")
				return
			}
			subscription.lastSent = message.Sequence
			c.mu.Unlock()
		}
		c.mu.RLock()
		lastSent := subscription.lastSent
		c.mu.RUnlock()
		if len(page.Messages) == 0 && lastSent < page.LatestSequence {
			_ = m.queueResume(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
				OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
			return
		}
		if lastSent >= page.LatestSequence {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			case <-c.done:
				return
			}
		}
		readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
		next, err := m.cfg.HistoryReader.ReadChannelHistory(readCtx, c.info.EndpointID, channel, lastSent, resumeHistoryPageSize)
		cancel()
		if err != nil {
			_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: channel, Code: "history_read_failed"})
			_ = c.close(websocket.CloseTryAgainLater, "resume history unavailable")
			return
		}
		page = next
	}
}

func (m *Manager) removeResumeSubscription(c *connection, channel string, subscription *resumeSubscription) {
	c.mu.Lock()
	if c.resumeSubs[channel] == subscription {
		delete(c.resumeSubs, channel)
		delete(c.channels, channel)
	}
	c.mu.Unlock()
	subscription.cancel()
}

func (m *Manager) resumeAck(ctx context.Context, c *connection, frame resumeClientFrame) {
	c.mu.Lock()
	subscription := c.resumeSubs[frame.Channel]
	if subscription == nil || frame.Sequence < subscription.lastAck || frame.Sequence > subscription.lastSent || frame.After != 0 {
		c.mu.Unlock()
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_ack"})
		return
	}
	subscription.lastAck = frame.Sequence
	c.mu.Unlock()
	_ = m.queueResume(ctx, c, resumeServerFrame{Type: "acknowledged", Channel: frame.Channel, Sequence: frame.Sequence})
}

func (m *Manager) resumeUnsubscribe(ctx context.Context, c *connection, channel string) {
	c.mu.RLock()
	subscription := c.resumeSubs[channel]
	c.mu.RUnlock()
	if subscription == nil {
		_ = m.queueResume(ctx, c, resumeServerFrame{Type: "error", Channel: channel, Code: "not_subscribed"})
		return
	}
	m.removeResumeSubscription(c, channel, subscription)
	_ = m.queueResume(ctx, c, resumeServerFrame{Type: "unsubscribed", Channel: channel})
}

func (m *Manager) queueResume(ctx context.Context, c *connection, frame resumeServerFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(frame)
	if err != nil || len(data) > maxResumeServerFrameBytes {
		return ErrOutboundQueueFull
	}
	select {
	case c.outbound <- Message{Data: data}:
		m.sentMessages.Add(1)
		m.sentBytes.Add(uint64(len(data)))
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrConnectionClosed
	default:
		m.droppedMessages.Add(1)
		return ErrOutboundQueueFull
	}
}
