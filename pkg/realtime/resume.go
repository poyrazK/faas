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
	ResumeSubprotocol = "gregale.realtime.v2"
	// ResumeBearerSubprotocolPrefix carries an OIDC JWT on browser v2
	// handshakes. The credential is removed before WebSocket negotiation and
	// never echoed as the selected subprotocol.
	ResumeBearerSubprotocolPrefix     = "gregale.realtime.bearer."
	maxResumeSubscriptions            = api.RealtimeResumeSubscriptionsPerNode
	maxResumePerConnection            = api.RealtimeResumeSubscriptionsPerConnection
	resumeHistoryPageSize             = state.ManagedRealtimeHistoryMaxRead
	resumeHistoryReadTimeout          = 5 * time.Second
	realtimeChannelRouteReportTimeout = 2 * time.Second
	maxResumeServerFrameBytes         = api.RealtimeResumeServerFrameMaxBytes
)

// ManagedRealtimeHistoryReader is the private apid RPC seam. A nil reader
// prevents v2 admission; realtimed never connects directly to PostgreSQL.
type ManagedRealtimeHistoryReader interface {
	ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error)
}

// ManagedRealtimeChannelRouteReporter lets resumable subscriptions update
// apid's shared channel route directory as soon as a route enters or leaves
// this node. Periodic route snapshots remain the recovery path for failures.
type ManagedRealtimeChannelRouteReporter interface {
	ReportChannelRoute(context.Context, string, string, bool) error
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
	wake     chan struct{}
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
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_frame"})
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
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_frame"})
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
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_subscription"})
		return
	}
	c.mu.RLock()
	_, exists := c.resumeSubs[frame.Channel]
	count := len(c.resumeSubs)
	c.mu.RUnlock()
	if exists || count >= maxResumePerConnection || !m.reserveResume() {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "subscription_limit"})
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
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_authorized"})
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	page, err := m.cfg.HistoryReader.ReadChannelHistory(readCtx, c.info.EndpointID, frame.Channel, frame.After, resumeHistoryPageSize)
	cancel()
	if err != nil {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "history_read_failed"})
		return
	}
	if page.HistoryUnavailable {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "resync_required", Channel: frame.Channel,
			OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
		return
	}
	subCtx, subCancel := context.WithCancel(ctx)
	subscription := &resumeSubscription{cancel: subCancel, wake: make(chan struct{}, 1), lastSent: frame.After, lastAck: frame.After}
	m.mu.Lock()
	if m.conns[c.info.ID] != c {
		m.mu.Unlock()
		subCancel()
		return
	}
	c.mu.Lock()
	if _, exists := c.resumeSubs[frame.Channel]; exists {
		c.mu.Unlock()
		m.mu.Unlock()
		subCancel()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "subscription_limit"})
		return
	}
	c.resumeSubs[frame.Channel] = subscription
	c.channels[frame.Channel] = struct{}{}
	key := channelKey{endpointID: c.info.EndpointID, channel: frame.Channel}
	hadSubscribers := m.hasChannelSubscribersLocked(key)
	if m.resumeSubscribers[key] == nil {
		m.resumeSubscribers[key] = make(map[string]struct{})
	}
	m.resumeSubscribers[key][c.info.ID] = struct{}{}
	if !hadSubscribers {
		m.channelRouteRevision++
	}
	c.mu.Unlock()
	m.mu.Unlock()
	if !hadSubscribers {
		m.reportChannelRoute(ctx, key, true)
	}
	if err := m.queueResume(ctx, c, resumeServerFrame{Type: "subscribed", Channel: frame.Channel,
		Sequence: frame.After, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}); err != nil {
		m.removeResumeSubscription(ctx, c, frame.Channel, subscription)
		m.closeResumeOnQueueFull(c, err)
		return
	}
	release = false
	go m.pumpResumeSubscription(subCtx, c, frame.Channel, subscription, page)
}

func (m *Manager) pumpResumeSubscription(ctx context.Context, c *connection, channel string, subscription *resumeSubscription, page state.ManagedRealtimeChannelHistory) {
	defer m.resumeCount.Add(-1)
	defer m.removeResumeSubscription(ctx, c, channel, subscription)
	ticker := time.NewTicker(m.cfg.ResumePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if page.HistoryUnavailable {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
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
				m.queueResumeControl(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
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
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
				OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
			return
		}
		if lastSent >= page.LatestSequence {
			select {
			case <-ticker.C:
			case <-subscription.wake:
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
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: channel, Code: "history_read_failed"})
			_ = c.close(websocket.CloseTryAgainLater, "resume history unavailable")
			return
		}
		page = next
	}
}

func (m *Manager) removeResumeSubscription(ctx context.Context, c *connection, channel string, subscription *resumeSubscription) {
	var routeRemoved bool
	key := channelKey{endpointID: c.info.EndpointID, channel: channel}
	m.mu.Lock()
	c.mu.Lock()
	if c.resumeSubs[channel] == subscription {
		delete(c.resumeSubs, channel)
		delete(c.channels, channel)
		hadSubscribers := m.hasChannelSubscribersLocked(key)
		members := m.resumeSubscribers[key]
		if _, subscribed := members[c.info.ID]; subscribed {
			delete(members, c.info.ID)
			if len(members) == 0 {
				delete(m.resumeSubscribers, key)
			}
			if hadSubscribers && !m.hasChannelSubscribersLocked(key) {
				m.channelRouteRevision++
				routeRemoved = true
			}
		}
	}
	c.mu.Unlock()
	m.mu.Unlock()
	subscription.cancel()
	if routeRemoved {
		m.reportChannelRoute(ctx, key, false)
	}
}

// reportChannelRoute serializes transitions and checks the current local
// membership before sending. If another subscribe or unsubscribe superseded
// this transition while it waited, the newer state is reported by that
// operation instead.
func (m *Manager) reportChannelRoute(ctx context.Context, key channelKey, subscribed bool) {
	reporter := m.cfg.ChannelRouteReporter
	if reporter == nil {
		return
	}
	m.routeReportMu.Lock()
	defer m.routeReportMu.Unlock()
	m.mu.RLock()
	current := m.hasChannelSubscribersLocked(key)
	m.mu.RUnlock()
	if current != subscribed {
		return
	}
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), realtimeChannelRouteReportTimeout)
	err := reporter.ReportChannelRoute(reportCtx, key.endpointID, key.channel, subscribed)
	cancel()
	if err != nil && m.cfg.ChannelRouteReportFailure != nil {
		m.cfg.ChannelRouteReportFailure(err)
	}
}

// PublishRetainedWithStatus wakes resumable subscribers after the message has
// been committed to the ordered history log. The durable reader remains the
// source of message bytes and ordering; the wake channel only reduces delivery
// latency, while the bounded poll remains the recovery path for missed wakes.
func (m *Manager) PublishRetainedWithStatus(ctx context.Context, endpointID, channel string, message Message, sequence int64) (PublishStatus, error) {
	if sequence <= 0 {
		return PublishStatus{}, ErrInvalidChannel
	}
	status, err := m.PublishWithStatus(ctx, endpointID, channel, message)
	if err != nil {
		return status, err
	}
	key := channelKey{endpointID: endpointID, channel: channel}
	m.mu.RLock()
	ids := make([]string, 0, len(m.resumeSubscribers[key]))
	for id := range m.resumeSubscribers[key] {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		c, ok := m.connection(id)
		if !ok {
			continue
		}
		select {
		case <-c.done:
			continue
		default:
		}
		c.mu.RLock()
		subscription := c.resumeSubs[channel]
		valid := subscription != nil && c.info.EndpointID == endpointID
		c.mu.RUnlock()
		if !valid {
			continue
		}
		select {
		case subscription.wake <- struct{}{}:
		default:
		}
		status.Subscribers++
		status.Queued++
	}
	return status, nil
}

func (m *Manager) resumeAck(ctx context.Context, c *connection, frame resumeClientFrame) {
	c.mu.Lock()
	subscription := c.resumeSubs[frame.Channel]
	if subscription == nil || frame.Sequence < subscription.lastAck || frame.Sequence > subscription.lastSent || frame.After != 0 {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_ack"})
		return
	}
	subscription.lastAck = frame.Sequence
	c.mu.Unlock()
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "acknowledged", Channel: frame.Channel, Sequence: frame.Sequence})
}

func (m *Manager) resumeUnsubscribe(ctx context.Context, c *connection, channel string) {
	c.mu.RLock()
	subscription := c.resumeSubs[channel]
	c.mu.RUnlock()
	if subscription == nil {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: channel, Code: "not_subscribed"})
		return
	}
	m.removeResumeSubscription(ctx, c, channel, subscription)
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "unsubscribed", Channel: channel})
}

// Control frames must either be queued or end the connection. Otherwise a
// slow client could miss a resynchronization signal and wait indefinitely on
// a channel that was already removed.
func (m *Manager) queueResumeControl(ctx context.Context, c *connection, frame resumeServerFrame) {
	m.closeResumeOnQueueFull(c, m.queueResume(ctx, c, frame))
}

func (m *Manager) closeResumeOnQueueFull(c *connection, err error) {
	if errors.Is(err, ErrOutboundQueueFull) {
		_ = c.close(websocket.CloseTryAgainLater, "resume output queue full")
	}
}

func (m *Manager) queueResume(ctx context.Context, c *connection, frame resumeServerFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(frame)
	if err != nil || len(data) > maxResumeServerFrameBytes {
		return ErrOutboundQueueFull
	}
	if err := m.queueOutbound(ctx, c, Message{Data: data}); err != nil {
		return err
	}
	m.sentMessages.Add(1)
	m.sentBytes.Add(uint64(len(data)))
	return nil
}
