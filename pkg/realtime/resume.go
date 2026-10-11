package realtime

import (
	"bytes"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	maxResumePresenceStateBytes       = ManagedRealtimePresenceStateMaxBytes
	maxResumeSignalDataBytes          = ManagedRealtimeSignalDataMaxBytes
	maxResumeSignalsPerSecond         = 20
	maxResumeChannelSignalsPerSecond  = 50
	maxResumePresenceUpdatesPerSecond = 10
	resumePresenceSnapshotChunk       = 12
	resumeOutputQueueFullReason       = "resume output queue full"
)

// ManagedRealtimeHistoryReader is the private apid RPC seam. A nil reader
// prevents v2 admission; realtimed never connects directly to PostgreSQL.
type ManagedRealtimeHistoryReader interface {
	ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error)
}

// ManagedRealtimeDurableCursorClient is the private apid RPC seam for
// authenticated server-managed checkpoints. It is optional so older history
// reader implementations continue to support client-held v2 cursors.
type ManagedRealtimeDurableCursorClient interface {
	LoadDurableCursor(context.Context, string, string, string, string, int64) (int64, error)
	AdvanceDurableCursor(context.Context, string, string, string, string, int64) (int64, error)
	ResetDurableCursor(context.Context, string, string, string, string, int64) (int64, error)
}

// ManagedRealtimeChannelRouteReporter lets resumable subscriptions update
// apid's shared channel route directory as soon as a route enters or leaves
// this node. Periodic route snapshots remain the recovery path for failures.
type ManagedRealtimeChannelRouteReporter interface {
	ReportChannelRoute(context.Context, string, string, bool) error
}

func (m *Manager) readResumeHistory(ctx context.Context, endpointID, channel string, after int64) (state.ManagedRealtimeChannelHistory, error) {
	started := time.Now()
	page, err := m.cfg.HistoryReader.ReadChannelHistory(ctx, endpointID, channel, after, resumeHistoryPageSize)
	result := resumeHistoryReadSuccess
	if err != nil {
		if errors.Is(err, context.Canceled) {
			result = resumeHistoryReadCanceled
		} else {
			result = resumeHistoryReadError
		}
	} else if page.HistoryUnavailable {
		result = resumeHistoryReadUnavailable
	}
	m.recordResumeHistoryRead(time.Since(started), result)
	return page, err
}

func (m *Manager) queueResumeResync(ctx context.Context, c *connection, frame resumeServerFrame, reason resumeResyncReason) {
	m.recordResumeResync(reason)
	m.queueResumeControl(ctx, c, frame)
}

// v2 cursors may be client-held or server-managed. Ack confirms a sequence
// actually sent on this connection; durable subscriptions persist that
// checkpoint before confirming the ack. A lost ack can replay a message, so
// consumers deduplicate by sequence.
type resumeClientFrame struct {
	Filter         map[string]string `json:"filter,omitempty"`
	ReadReceipts   bool              `json:"read_receipts,omitempty"`
	Name           string            `json:"name,omitempty"`
	TTLMS          int               `json:"ttl_ms,omitempty"`
	Consumer       string            `json:"consumer,omitempty"`
	Type           string            `json:"type"`
	Channel        string            `json:"channel"`
	Subscription   string            `json:"subscription,omitempty"`
	PresenceScope  string            `json:"presence_scope,omitempty"`
	DirectMessages bool              `json:"direct_messages,omitempty"`
	MessageID      string            `json:"message_id,omitempty"`
	After          int64             `json:"after,omitempty"`
	Sequence       int64             `json:"sequence,omitempty"`
	State          json.RawMessage   `json:"state,omitempty"`
	Data           json.RawMessage   `json:"data,omitempty"`
}

type resumeServerFrame struct {
	Metadata           map[string]string                    `json:"metadata,omitempty"`
	Preferences        *api.RealtimeNotificationPreferences `json:"preferences,omitempty"`
	Inbox              bool                                 `json:"inbox,omitempty"`
	Unread             int64                                `json:"unread,omitempty"`
	HistoryUnavailable bool                                 `json:"history_unavailable,omitempty"`
	TargetMessageID    string                               `json:"target_message_id,omitempty"`
	Version            int64                                `json:"version,omitempty"`
	MessageEvent       string                               `json:"message_event,omitempty"`
	Deleted            bool                                 `json:"deleted,omitempty"`
	Name               string                               `json:"name,omitempty"`
	ExpiresAt          *time.Time                           `json:"expires_at,omitempty"`
	Consumer           string                               `json:"consumer,omitempty"`
	Type               string                               `json:"type"`
	Channel            string                               `json:"channel,omitempty"`
	Subscription       string                               `json:"subscription,omitempty"`
	Code               string                               `json:"code,omitempty"`
	Sequence           int64                                `json:"sequence,omitempty"`
	OldestSequence     int64                                `json:"oldest_sequence,omitempty"`
	LatestSequence     int64                                `json:"latest_sequence,omitempty"`
	MessageID          string                               `json:"message_id,omitempty"`
	ReceiptRequested   bool                                 `json:"receipt_requested,omitempty"`
	DataBase64         string                               `json:"data_base64,omitempty"`
	Binary             bool                                 `json:"binary,omitempty"`
	Event              string                               `json:"event,omitempty"`
	MemberID           string                               `json:"member_id,omitempty"`
	State              json.RawMessage                      `json:"state,omitempty"`
	Data               json.RawMessage                      `json:"data,omitempty"`
	ConnectionCount    int                                  `json:"connection_count,omitempty"`
	Members            []resumePresenceMember               `json:"members,omitempty"`
	Complete           bool                                 `json:"complete,omitempty"`
	UpdatedAt          time.Time                            `json:"updated_at,omitempty"`
}

type resumePresenceMember struct {
	MemberID        string          `json:"member_id"`
	State           json.RawMessage `json:"state"`
	ConnectionCount int             `json:"connection_count,omitempty"`
}

type presenceMember struct {
	memberID      string
	presenceScope string
	state         json.RawMessage
	updatedAt     time.Time
	announced     bool
}

type resumeSignalRate struct {
	window time.Time
	count  int
}

type resumeSubscription struct {
	filter      map[string]string
	cancel      context.CancelFunc
	wake        chan struct{}
	durableName string
	lastSent    int64 // guarded by connection.mu
	lastAck     int64 // guarded by connection.mu
	resyncing   bool  // guarded by connection.mu
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
		case "inbox_subscribe":
			m.resumeInboxSubscribe(ctx, c, frame)
		case "inbox_preferences_get", "inbox_preferences_put":
			m.resumeInboxNotificationPreferences(ctx, c, frame)
		case "inbox_push_register", "inbox_push_unregister":
			m.resumeInboxPush(ctx, c, frame)
		case "inbox_ack":
			m.resumeInboxAck(ctx, c, frame)
		case "inbox_reset":
			m.resumeInboxReset(ctx, c, frame)
		case "subscribe":
			m.resumeSubscribe(ctx, c, frame)
		case "ack":
			m.resumeAck(ctx, c, frame)
		case "direct_message_ack":
			m.resumeDirectMessageAck(ctx, c, frame)
		case "reset":
			m.resumeReset(ctx, c, frame)
		case "unsubscribe":
			m.resumeUnsubscribe(ctx, c, frame.Channel)
		case "presence":
			m.resumePresenceUpdate(ctx, c, frame)
		case "read":
			m.resumeReadProgress(ctx, c, frame, false, true)
		case "read_state":
			m.resumeReadProgress(ctx, c, frame, false, false)
		case "inbox_read":
			m.resumeReadProgress(ctx, c, frame, true, true)
		case "inbox_read_state":
			m.resumeReadProgress(ctx, c, frame, true, false)
		case "signal":
			m.resumeSignal(ctx, c, frame)
		default:
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_frame"})
		}
	}
}

func (m *Manager) resumeDirectMessageAck(ctx context.Context, c *connection, frame resumeClientFrame) {
	if api.ValidateRealtimeDirectMessageID(frame.MessageID) != nil {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_direct_message_ack", MessageID: frame.MessageID})
		return
	}
	c.mu.Lock()
	if !c.v2 || !c.directMessages {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_direct_message_ack", MessageID: frame.MessageID})
		return
	}
	now := time.Now().UTC()
	for messageID, ackedAt := range c.recentDirectMessageAcks {
		if now.Sub(ackedAt) > 2*time.Minute {
			delete(c.recentDirectMessageAcks, messageID)
		}
	}
	if _, exists := c.recentDirectMessageAcks[frame.MessageID]; exists {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "direct_message_acknowledged", MessageID: frame.MessageID})
		return
	}
	if _, exists := c.pendingDirectMessages[frame.MessageID]; !exists {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Code: "invalid_direct_message_ack", MessageID: frame.MessageID})
		return
	}
	c.mu.Unlock()
	receiptClient, ok := m.cfg.FleetClient.(ManagedRealtimeDirectMessageReceiptClient)
	if !ok {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "direct_message_ack_failed", MessageID: frame.MessageID})
		return
	}
	ackCtx, cancel := context.WithTimeout(ctx, realtimeChannelRouteReportTimeout)
	err := receiptClient.AcknowledgeDirectMessage(ackCtx, c.info.EndpointID, frame.MessageID, c.info.ID)
	cancel()
	if err != nil {
		m.reportFleetFailure(err)
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "direct_message_ack_failed", MessageID: frame.MessageID})
		return
	}
	c.mu.Lock()
	delete(c.pendingDirectMessages, frame.MessageID)
	if len(c.recentDirectMessageAcks) >= 1024 {
		var oldestID string
		var oldestAt time.Time
		for messageID, ackedAt := range c.recentDirectMessageAcks {
			if oldestID == "" || ackedAt.Before(oldestAt) {
				oldestID, oldestAt = messageID, ackedAt
			}
		}
		delete(c.recentDirectMessageAcks, oldestID)
	}
	c.recentDirectMessageAcks[frame.MessageID] = time.Now().UTC()
	c.mu.Unlock()
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "direct_message_acknowledged", MessageID: frame.MessageID})
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
	if api.ValidateRealtimeMetadata(frame.Filter) != nil || !validChannel(frame.Channel) || frame.After < 0 || frame.Sequence != 0 ||
		(frame.Subscription != "" && !validDurableSubscription(frame.Subscription)) ||
		(frame.PresenceScope != "" && frame.PresenceScope != "connection" && frame.PresenceScope != "principal") {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_subscription"})
		return
	}
	if frame.PresenceScope == "principal" {
		if c.info.Principal == "" {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "presence_identity_requires_identity"})
			return
		}
		if m.cfg.FleetClient == nil {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "presence_identity_unavailable"})
			return
		}
	}
	presenceState, validPresence := normalizeResumePresenceState(frame.State)
	if !validPresence {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_presence"})
		return
	}
	c.mu.RLock()
	_, exists := c.resumeSubs[frame.Channel]
	count := len(c.resumeSubs)
	if c.inboxSub != nil {
		count++
	}
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
	if !m.authorizeResumeChannel(ctx, c, frame.Channel) {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_authorized"})
		return
	}
	after := frame.After
	if frame.Subscription != "" {
		if c.info.Principal == "" {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "durable_subscription_requires_identity"})
			return
		}
		cursorStore, ok := m.cfg.HistoryReader.(ManagedRealtimeDurableCursorClient)
		if !ok {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "durable_subscriptions_unavailable"})
			return
		}
		cursorCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
		var err error
		after, err = cursorStore.LoadDurableCursor(cursorCtx, c.info.EndpointID, c.info.Principal, frame.Subscription, frame.Channel, frame.After)
		cancel()
		if err != nil {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: durableCursorErrorCode(err)})
			return
		}
	}
	readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	page, err := m.readResumeHistory(readCtx, c.info.EndpointID, frame.Channel, after)
	cancel()
	if err != nil {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "history_read_failed"})
		return
	}
	if page.HistoryUnavailable {
		m.queueResumeResync(ctx, c, resumeServerFrame{Type: "resync_required", Channel: frame.Channel,
			Subscription: frame.Subscription, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}, resumeResyncCursorExpired)
		return
	}
	var fleetMember PresenceMember
	var fleetSnapshot []PresenceMember
	fleetPresenceReady := m.cfg.FleetClient != nil
	if fleetPresenceReady {
		principal := ""
		if frame.PresenceScope == "principal" {
			principal = c.info.Principal
		}
		fleetMember, fleetSnapshot, err = m.upsertFleetPresence(ctx, c, frame.Channel, c.presenceID, principal, presenceState)
		if err != nil {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: fleetPresenceErrorCode(err)})
			return
		}
	} else {
		fleetMember = PresenceMember{MemberID: c.presenceID, State: append(json.RawMessage(nil), presenceState...), ConnectionCount: 1, UpdatedAt: time.Now().UTC()}
	}
	subCtx, subCancel := context.WithCancel(ctx)
	filter := map[string]string{}
	for key, value := range frame.Filter {
		filter[key] = value
	}
	subscription := &resumeSubscription{filter: filter, cancel: subCancel, wake: make(chan struct{}, 1), durableName: frame.Subscription, lastSent: after, lastAck: after}
	m.mu.Lock()
	if m.conns[c.info.ID] != c {
		m.mu.Unlock()
		subCancel()
		if fleetPresenceReady {
			m.deleteFleetPresence(ctx, c, frame.Channel, fleetMember.MemberID)
		}
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
	if frame.ReadReceipts {
		c.readReceipts = true
	}
	if frame.DirectMessages {
		c.directMessages = true
	}
	key := channelKey{endpointID: c.info.EndpointID, channel: frame.Channel}
	hadSubscribers := m.hasChannelSubscribersLocked(key)
	var presenceSnapshot []resumePresenceMember
	var previousFleetPresence map[string]PresenceMember
	if fleetPresenceReady {
		if existing := m.fleetPresence[key]; existing != nil {
			previousFleetPresence = make(map[string]PresenceMember, len(existing))
			for memberID, member := range existing {
				previousFleetPresence[memberID] = clonePresenceMember(member)
			}
		}
		m.installFleetPresenceSnapshotLocked(key, fleetSnapshot)
		if current, ok := m.fleetPresence[key][fleetMember.MemberID]; ok && current.UpdatedAt.After(fleetMember.UpdatedAt) {
			fleetMember = clonePresenceMember(current)
		}
		for _, member := range fleetSnapshot {
			if member.MemberID == fleetMember.MemberID {
				continue
			}
			current, exists := m.fleetPresence[key][member.MemberID]
			if !exists {
				continue
			}
			member = current
			presenceSnapshot = append(presenceSnapshot, resumePresenceMember{
				MemberID: member.MemberID, State: append(json.RawMessage(nil), member.State...), ConnectionCount: member.ConnectionCount,
			})
		}
	} else {
		for _, member := range m.presenceMembers[key] {
			if !member.announced {
				continue
			}
			presenceSnapshot = append(presenceSnapshot, resumePresenceMember{
				MemberID: member.memberID, State: append(json.RawMessage(nil), member.state...), ConnectionCount: 1,
			})
		}
	}
	if m.presenceMembers[key] == nil {
		m.presenceMembers[key] = make(map[string]*presenceMember)
	}
	newMember := &presenceMember{memberID: fleetMember.MemberID, presenceScope: frame.PresenceScope, state: presenceState, updatedAt: fleetMember.UpdatedAt}
	m.presenceMembers[key][c.info.ID] = newMember
	m.setFleetPresenceLocked(key, fleetMember)
	if m.resumeSubscribers[key] == nil {
		m.resumeSubscribers[key] = make(map[string]struct{})
	}
	m.resumeSubscribers[key][c.info.ID] = struct{}{}
	if !hadSubscribers {
		m.channelRouteRevision++
	}
	c.mu.Unlock()
	queueErr := m.queueResume(ctx, c, resumeServerFrame{Type: "subscribed", Channel: frame.Channel,
		Subscription: frame.Subscription, Sequence: after, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
	if queueErr == nil {
		queueErr = m.queuePresenceSnapshot(ctx, c, frame.Channel, presenceSnapshot)
	}
	var fullPresenceQueues []*connection
	if fleetPresenceReady {
		for _, member := range fleetSnapshot {
			if member.MemberID == fleetMember.MemberID {
				continue
			}
			current, exists := m.fleetPresence[key][member.MemberID]
			if !exists || member.UpdatedAt.Before(current.UpdatedAt) {
				continue
			}
			previous, existed := previousFleetPresence[member.MemberID]
			if existed && previous.UpdatedAt.After(member.UpdatedAt) {
				continue
			}
			if existed && bytes.Equal(previous.State, current.State) && previous.ConnectionCount == current.ConnectionCount {
				continue
			}
			event := "joined"
			if existed {
				event = "updated"
			}
			fullPresenceQueues = append(fullPresenceQueues, m.queueEphemeralToSubscribersLocked(ctx, key, c.info.ID, resumeServerFrame{
				Type: "presence", Channel: frame.Channel, Event: event, MemberID: current.MemberID,
				State: append(json.RawMessage(nil), current.State...), ConnectionCount: current.ConnectionCount, UpdatedAt: current.UpdatedAt,
			})...)
		}
	}
	if queueErr == nil {
		newMember.announced = true
		presenceEvent := "joined"
		if frame.PresenceScope == "principal" && fleetMember.ConnectionCount > 1 {
			presenceEvent = "updated"
		}
		fullPresenceQueues = append(fullPresenceQueues, m.queueEphemeralToSubscribersLocked(ctx, key, c.info.ID, resumeServerFrame{
			Type: "presence", Channel: frame.Channel, Event: presenceEvent, MemberID: fleetMember.MemberID,
			State: append(json.RawMessage(nil), fleetMember.State...), ConnectionCount: fleetMember.ConnectionCount, UpdatedAt: fleetMember.UpdatedAt,
		})...)
	}
	m.mu.Unlock()
	if queueErr != nil {
		for _, slow := range fullPresenceQueues {
			m.closeResumeOnQueueFull(slow, ErrOutboundQueueFull)
		}
		m.removeResumeSubscription(ctx, c, frame.Channel, subscription)
		m.closeResumeOnQueueFull(c, queueErr)
		return
	}
	for _, slow := range fullPresenceQueues {
		m.closeResumeOnQueueFull(slow, ErrOutboundQueueFull)
	}
	if !hadSubscribers {
		m.reportChannelRoute(ctx, key, true)
	}
	presenceEvent := "joined"
	if frame.PresenceScope == "principal" && fleetMember.ConnectionCount > 1 {
		presenceEvent = "updated"
	}
	if err := m.relayFleetEphemeral(ctx, c.info.EndpointID, frame.Channel, EphemeralFrame{
		Type: "presence", Event: presenceEvent, MemberID: fleetMember.MemberID,
		State: append(json.RawMessage(nil), fleetMember.State...), ConnectionCount: fleetMember.ConnectionCount, UpdatedAt: fleetMember.UpdatedAt,
	}); err != nil {
		m.reportFleetFailure(err)
	}
	m.syncFleetPresence(ctx, key)
	release = false
	go m.pumpResumeSubscription(subCtx, c, frame.Channel, subscription, page)
}

func normalizeResumePresenceState(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), true
	}
	if len(raw) > maxResumePresenceStateBytes || !json.Valid(raw) {
		return nil, false
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, false
	}
	return append(json.RawMessage(nil), raw...), true
}

func fleetPresenceErrorCode(err error) string {
	if status.Code(err) == codes.ResourceExhausted {
		return "presence_limit"
	}
	return "presence_unavailable"
}

func normalizeResumeSignalData(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 || len(raw) > maxResumeSignalDataBytes || !json.Valid(raw) {
		return nil, false
	}
	return append(json.RawMessage(nil), raw...), true
}

func (m *Manager) queuePresenceSnapshot(ctx context.Context, c *connection, channel string, members []resumePresenceMember) error {
	if len(members) == 0 {
		return m.queueResume(ctx, c, resumeServerFrame{Type: "presence", Channel: channel, Event: "snapshot", Members: []resumePresenceMember{}, Complete: true})
	}
	for start := 0; start < len(members); start += resumePresenceSnapshotChunk {
		end := min(start+resumePresenceSnapshotChunk, len(members))
		if err := m.queueResume(ctx, c, resumeServerFrame{
			Type: "presence", Channel: channel, Event: "snapshot",
			Members: members[start:end], Complete: end == len(members),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) resumePresenceUpdate(ctx context.Context, c *connection, frame resumeClientFrame) {
	state, valid := normalizeResumePresenceState(frame.State)
	if !valid || !validChannel(frame.Channel) || frame.Data != nil || frame.After != 0 || frame.Sequence != 0 || frame.Subscription != "" || frame.PresenceScope != "" || frame.DirectMessages {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_presence"})
		return
	}
	m.mu.RLock()
	if m.conns[c.info.ID] != c {
		m.mu.RUnlock()
		return
	}
	c.mu.RLock()
	if c.resumeSubs[frame.Channel] == nil {
		c.mu.RUnlock()
		m.mu.RUnlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_subscribed"})
		return
	}
	key := channelKey{endpointID: c.info.EndpointID, channel: frame.Channel}
	member := m.presenceMembers[key][c.info.ID]
	if member == nil || !member.announced {
		c.mu.RUnlock()
		m.mu.RUnlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_subscribed"})
		return
	}
	memberID := member.memberID
	presenceScope := member.presenceScope
	c.mu.RUnlock()
	m.mu.RUnlock()

	c.mu.Lock()
	now := time.Now()
	if c.presenceWindow.IsZero() || now.Sub(c.presenceWindow) >= time.Second {
		c.presenceWindow = now
		c.presenceCount = 0
	}
	if c.presenceCount >= maxResumePresenceUpdatesPerSecond {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "presence_rate_limited"})
		return
	}
	c.presenceCount++
	c.mu.Unlock()
	var fleetMember PresenceMember
	if m.cfg.FleetClient != nil {
		var err error
		principal := ""
		if presenceScope == "principal" {
			principal = c.info.Principal
		}
		fleetMember, _, err = m.upsertFleetPresence(ctx, c, frame.Channel, memberID, principal, state)
		if err != nil {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: fleetPresenceErrorCode(err)})
			return
		}
	} else {
		fleetMember = PresenceMember{MemberID: memberID, State: append(json.RawMessage(nil), state...), ConnectionCount: 1, UpdatedAt: now.UTC()}
	}
	m.mu.Lock()
	if m.conns[c.info.ID] != c {
		m.mu.Unlock()
		return
	}
	c.mu.Lock()
	member = m.presenceMembers[key][c.info.ID]
	if c.resumeSubs[frame.Channel] == nil || member == nil || !member.announced || member.memberID != memberID {
		c.mu.Unlock()
		m.mu.Unlock()
		return
	}
	member.state = state
	member.updatedAt = fleetMember.UpdatedAt
	if current, exists := m.fleetPresence[key][fleetMember.MemberID]; exists && current.UpdatedAt.After(fleetMember.UpdatedAt) {
		c.mu.Unlock()
		m.mu.Unlock()
		return
	}
	m.setFleetPresenceLocked(key, fleetMember)
	fullQueues := m.queueEphemeralToSubscribersLocked(ctx, key, c.info.ID, resumeServerFrame{
		Type: "presence", Channel: frame.Channel, Event: "updated", MemberID: fleetMember.MemberID,
		State: append(json.RawMessage(nil), fleetMember.State...), ConnectionCount: fleetMember.ConnectionCount, UpdatedAt: fleetMember.UpdatedAt,
	})
	c.mu.Unlock()
	m.mu.Unlock()
	for _, slow := range fullQueues {
		m.closeResumeOnQueueFull(slow, ErrOutboundQueueFull)
	}
	if err := m.relayFleetEphemeral(ctx, c.info.EndpointID, frame.Channel, EphemeralFrame{
		Type: "presence", Event: "updated", MemberID: fleetMember.MemberID,
		State: append(json.RawMessage(nil), fleetMember.State...), ConnectionCount: fleetMember.ConnectionCount, UpdatedAt: fleetMember.UpdatedAt,
	}); err != nil {
		m.reportFleetFailure(err)
	}
}

func (m *Manager) resumeSignal(ctx context.Context, c *connection, frame resumeClientFrame) {
	data, valid := normalizeResumeSignalData(frame.Data)
	if !valid || (frame.Name == "" && frame.TTLMS != 0) || (frame.Name != "" && (!validSignalName(frame.Name) || frame.TTLMS < 0 || frame.TTLMS > int(ManagedRealtimeSignalMaxTTL/time.Millisecond))) || !validChannel(frame.Channel) || frame.State != nil || frame.After != 0 || frame.Sequence != 0 || frame.Subscription != "" || frame.DirectMessages || frame.Consumer != "" || frame.MessageID != "" || frame.PresenceScope != "" {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_signal"})
		return
	}
	now := time.Now()
	c.mu.Lock()
	if c.resumeSubs[frame.Channel] == nil {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_subscribed"})
		return
	}
	if c.signalWindow.IsZero() || now.Sub(c.signalWindow) >= time.Second {
		c.signalWindow = now
		c.signalCount = 0
	}
	if c.signalCount >= maxResumeSignalsPerSecond {
		c.mu.Unlock()
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "signal_rate_limited"})
		return
	}
	c.signalCount++
	c.mu.Unlock()
	key := channelKey{endpointID: c.info.EndpointID, channel: frame.Channel}
	memberID, code := m.takeResumeChannelSignal(key, c.info.ID, now)
	if code != "" {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: code})
		return
	}
	var expires *time.Time
	updated := time.Now().UTC()
	if frame.Name != "" {
		deadline := updated.Add(time.Duration(frame.TTLMS) * time.Millisecond)
		expires = &deadline
	}
	m.broadcastEphemeral(ctx, key, c.info.ID, resumeServerFrame{
		Type: "signal", Channel: frame.Channel, MemberID: memberID, Data: data, Name: frame.Name, ExpiresAt: expires, UpdatedAt: updated,
	})
	if err := m.relayFleetEphemeral(ctx, c.info.EndpointID, frame.Channel, EphemeralFrame{
		Type: "signal", MemberID: memberID, Data: append(json.RawMessage(nil), data...), Name: frame.Name, ExpiresAt: expires, UpdatedAt: updated,
	}); err != nil {
		if m.cfg.FleetClient != nil {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "signal_relay_unavailable"})
		}
	}
}

func (m *Manager) takeResumeChannelSignal(key channelKey, connectionID string, now time.Time) (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	member := m.presenceMembers[key][connectionID]
	if _, subscribed := m.resumeSubscribers[key][connectionID]; !subscribed || member == nil || !member.announced {
		return "", "not_subscribed"
	}
	rate := m.resumeSignalRates[key]
	if rate == nil {
		rate = &resumeSignalRate{}
		m.resumeSignalRates[key] = rate
	}
	if rate.window.IsZero() || now.Sub(rate.window) >= time.Second {
		rate.window = now
		rate.count = 0
	}
	if rate.count >= maxResumeChannelSignalsPerSecond {
		return "", "signal_rate_limited"
	}
	rate.count++
	return member.memberID, ""
}

// queueEphemeralToSubscribersLocked queues an event while m.mu is held. This
// keeps a new subscriber's snapshot and the join events seen by its peers in a
// single order relative to concurrent subscriptions.
func (m *Manager) queueEphemeralToSubscribersLocked(ctx context.Context, key channelKey, excludeConnectionID string, frame resumeServerFrame) []*connection {
	var fullQueues []*connection
	for id := range m.resumeSubscribers[key] {
		if id == excludeConnectionID {
			continue
		}
		member := m.presenceMembers[key][id]
		c := m.conns[id]
		if member == nil || !member.announced || c == nil || !c.v2 {
			continue
		}
		if err := m.queueResume(ctx, c, frame); errors.Is(err, ErrOutboundQueueFull) {
			fullQueues = append(fullQueues, c)
		}
	}
	return fullQueues
}

func (m *Manager) broadcastEphemeral(ctx context.Context, key channelKey, excludeConnectionID string, frame resumeServerFrame) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.resumeSubscribers[key]))
	for id := range m.resumeSubscribers[key] {
		member := m.presenceMembers[key][id]
		if id != excludeConnectionID && member != nil && member.announced {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range ids {
		c, ok := m.connection(id)
		if !ok || !c.v2 {
			continue
		}
		c.mu.RLock()
		subscribed := c.resumeSubs[key.channel] != nil && c.info.EndpointID == key.endpointID && (frame.Type != "read_receipt" || c.readReceipts)
		c.mu.RUnlock()
		if !subscribed {
			continue
		}
		if err := m.queueResume(ctx, c, frame); err != nil {
			m.closeResumeOnQueueFull(c, err)
		}
	}
}

func durableCursorErrorCode(err error) string {
	switch status.Code(err) {
	case codes.ResourceExhausted:
		return "subscription_limit"
	case codes.InvalidArgument:
		return "invalid_subscription"
	case codes.NotFound, codes.FailedPrecondition:
		return "durable_subscription_unavailable"
	default:
		return "durable_subscription_unavailable"
	}
}

func (m *Manager) authorizeResumeChannel(ctx context.Context, c *connection, channel string) bool {
	authorizer := m.hooks.(ChannelAuthorizer) // checked before WebSocket upgrade
	event := Event{
		ID: "evt_" + uuid.NewString(), Type: EventAuthorizeChannel,
		EndpointID: c.info.EndpointID, AppID: c.info.AppID, AccountID: c.info.AccountID,
		ConnectionID: c.info.ID, Principal: c.info.Principal, Channel: channel,
		Permission: "read", At: time.Now().UTC(), CallbackURL: c.endpoint.CallbackURL,
		CallbackAuthToken: c.currentCallbackAuthToken(),
	}
	parent, scope, parseErr := api.ParseRealtimeActivityScope(channel)
	if parseErr != nil {
		return false
	}
	if scope != "" {
		event.ActivityParentChannel = parent
		event.ActivityScope = scope
		event.Permission = "read_activity"
	}
	authCtx, cancel := context.WithTimeout(ctx, m.cfg.CallbackTimeout)
	allowed, err := authorizer.AuthorizeChannel(authCtx, event)
	cancel()
	return err == nil && allowed
}

func (m *Manager) pumpResumeSubscription(ctx context.Context, c *connection, channel string, subscription *resumeSubscription, page state.ManagedRealtimeChannelHistory) {
	defer m.resumeCount.Add(-1)
	defer m.removeResumeSubscription(ctx, c, channel, subscription)
	ticker := time.NewTicker(m.cfg.ResumePollInterval)
	defer ticker.Stop()
	lastLeaseRenewal := time.Now()
	key := channelKey{endpointID: c.info.EndpointID, channel: channel}
	for {
		if ctx.Err() != nil {
			return
		}
		if page.HistoryUnavailable {
			m.markResumeSubscriptionResync(c, channel, subscription)
			m.queueResumeResync(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
				Subscription: subscription.durableName, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}, resumeResyncRetentionAdvanced)
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
				m.markResumeSubscriptionResync(c, channel, subscription)
				m.queueResumeResync(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
					Subscription: subscription.durableName, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}, resumeResyncSequenceGap)
				return
			}
			matches := true
			for key, value := range subscription.filter {
				actual, exists := message.Metadata[key]
				if !exists || actual != value {
					matches = false
					break
				}
			}
			if !matches {
				if err := m.queueResume(ctx, c, resumeServerFrame{Type: "checkpoint", Channel: channel, Sequence: message.Sequence}); err != nil {
					c.mu.Unlock()
					_ = c.close(websocket.CloseTryAgainLater, resumeOutputQueueFullReason)
					return
				}
				subscription.lastSent = message.Sequence
				c.mu.Unlock()
				continue
			}
			if err := m.queueResume(ctx, c, resumeServerFrame{
				Metadata: message.Metadata, Type: "message", Channel: channel, Sequence: message.Sequence, TargetMessageID: message.TargetMessageID, Version: message.Version, MessageEvent: message.MessageEvent, Deleted: message.Deleted,
				MessageID:  uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.info.EndpointID+"\x00"+channel+"\x00"+strconv.FormatInt(message.Sequence, 10))).String(),
				DataBase64: base64.StdEncoding.EncodeToString(message.Data), Binary: message.Binary,
			}); err != nil {
				c.mu.Unlock()
				_ = c.close(websocket.CloseTryAgainLater, resumeOutputQueueFullReason)
				return
			}
			m.recordResumeMessageQueued(len(message.Data))
			subscription.lastSent = message.Sequence
			c.mu.Unlock()
		}
		c.mu.RLock()
		lastSent := subscription.lastSent
		c.mu.RUnlock()
		if len(page.Messages) == 0 && lastSent < page.LatestSequence {
			m.markResumeSubscriptionResync(c, channel, subscription)
			m.queueResumeResync(ctx, c, resumeServerFrame{Type: "resync_required", Channel: channel,
				Subscription: subscription.durableName, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}, resumeResyncSequenceGap)
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
		m.syncFleetPresence(ctx, key)
		if m.cfg.FleetClient != nil && time.Since(lastLeaseRenewal) >= m.cfg.PresenceLeaseRenewInterval {
			m.renewFleetPresence(ctx, c, channel)
			lastLeaseRenewal = time.Now()
		}
		readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
		next, err := m.readResumeHistory(readCtx, c.info.EndpointID, channel, lastSent)
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
	var memberID string
	var leaseDeleted bool
	var fullPresenceQueues []*connection
	key := channelKey{endpointID: c.info.EndpointID, channel: channel}
	m.mu.Lock()
	c.mu.Lock()
	if c.resumeSubs[channel] == subscription {
		delete(c.resumeSubs, channel)
		delete(c.channels, channel)
		if member := m.presenceMembers[key][c.info.ID]; member != nil {
			leaseDeleted = true
			if m.cfg.FleetClient == nil {
				leftAt := time.Now().UTC()
				delete(m.fleetPresence[key], member.memberID)
				m.recordFleetPresenceTombstoneLocked(key, member.memberID, leftAt)
			}
			if member.announced {
				memberID = member.memberID
			}
			delete(m.presenceMembers[key], c.info.ID)
			if len(m.presenceMembers[key]) == 0 {
				delete(m.presenceMembers, key)
			}
		}
		hadSubscribers := m.hasChannelSubscribersLocked(key)
		members := m.resumeSubscribers[key]
		if _, subscribed := members[c.info.ID]; subscribed {
			delete(members, c.info.ID)
			if len(members) == 0 {
				delete(m.resumeSubscribers, key)
				delete(m.resumeSignalRates, key)
				delete(m.fleetPresence, key)
				delete(m.fleetPresenceTombstones, key)
				delete(m.fleetPresenceLastSync, key)
			}
			if hadSubscribers && !m.hasChannelSubscribersLocked(key) {
				m.channelRouteRevision++
				routeRemoved = true
				delete(m.resumeSignalRates, key)
			}
		}
	}
	if memberID != "" && m.cfg.FleetClient == nil {
		fullPresenceQueues = m.queueEphemeralToSubscribersLocked(ctx, key, c.info.ID,
			resumeServerFrame{Type: "presence", Channel: channel, Event: "left", MemberID: memberID, UpdatedAt: time.Now().UTC()})
	}
	c.mu.Unlock()
	m.mu.Unlock()
	subscription.cancel()
	for _, slow := range fullPresenceQueues {
		m.closeResumeOnQueueFull(slow, ErrOutboundQueueFull)
	}
	if routeRemoved {
		m.reportChannelRoute(ctx, key, false)
	}
	if leaseDeleted {
		m.deleteFleetPresence(ctx, c, channel, memberID)
	}
}

func (m *Manager) markResumeSubscriptionResync(c *connection, channel string, subscription *resumeSubscription) {
	c.mu.Lock()
	if c.resumeSubs[channel] == subscription {
		subscription.resyncing = true
	}
	c.mu.Unlock()
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
	durableName := subscription.durableName
	principal := c.info.Principal
	c.mu.Unlock()
	if durableName != "" {
		cursorStore, ok := m.cfg.HistoryReader.(ManagedRealtimeDurableCursorClient)
		if !ok || principal == "" {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "durable_subscriptions_unavailable"})
			_ = c.close(websocket.CloseTryAgainLater, "durable checkpoint unavailable")
			return
		}
		ackCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
		_, err := cursorStore.AdvanceDurableCursor(ackCtx, c.info.EndpointID, principal, durableName, frame.Channel, frame.Sequence)
		cancel()
		if err != nil {
			m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "checkpoint_write_failed"})
			_ = c.close(websocket.CloseTryAgainLater, "durable checkpoint write failed")
			return
		}
	}
	c.mu.Lock()
	if c.resumeSubs[frame.Channel] != subscription {
		c.mu.Unlock()
		return
	}
	if frame.Sequence > subscription.lastAck {
		subscription.lastAck = frame.Sequence
	}
	c.mu.Unlock()
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "acknowledged", Channel: frame.Channel,
		Subscription: durableName, Sequence: frame.Sequence})
}

func (m *Manager) resumeReset(ctx context.Context, c *connection, frame resumeClientFrame) {
	if !validChannel(frame.Channel) || !validDurableSubscription(frame.Subscription) || frame.After != 0 || frame.Sequence < 0 {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "invalid_cursor_reset"})
		return
	}
	if c.info.Principal == "" {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "durable_subscription_requires_identity"})
		return
	}
	c.mu.RLock()
	active := c.resumeSubs[frame.Channel]
	c.mu.RUnlock()
	if active != nil && (active.durableName != frame.Subscription || !active.resyncing) {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "cursor_reset_not_allowed"})
		return
	}
	if active != nil {
		m.removeResumeSubscription(ctx, c, frame.Channel, active)
	}
	if !m.authorizeResumeChannel(ctx, c, frame.Channel) {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "not_authorized"})
		return
	}
	cursorStore, ok := m.cfg.HistoryReader.(ManagedRealtimeDurableCursorClient)
	if !ok {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "durable_subscriptions_unavailable"})
		return
	}
	resetCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	_, err := cursorStore.ResetDurableCursor(resetCtx, c.info.EndpointID, c.info.Principal, frame.Subscription, frame.Channel, frame.Sequence)
	cancel()
	if err != nil {
		if code := status.Code(err); code == codes.FailedPrecondition || code == codes.InvalidArgument {
			readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
			page, readErr := m.readResumeHistory(readCtx, c.info.EndpointID, frame.Channel, 0)
			cancel()
			if readErr == nil {
				m.queueResumeResync(ctx, c, resumeServerFrame{Type: "resync_required", Channel: frame.Channel,
					Subscription: frame.Subscription, OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence}, resumeResyncCursorExpired)
				return
			}
		}
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "error", Channel: frame.Channel, Code: "cursor_reset_failed"})
		return
	}
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "subscription_reset", Channel: frame.Channel,
		Subscription: frame.Subscription, Sequence: frame.Sequence})
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
		_ = c.close(websocket.CloseTryAgainLater, resumeOutputQueueFullReason)
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
	message := Message{Data: data}
	if frame.Type == "signal" && frame.Name != "" {
		message.signalKey = pendingSignalKey{channel: frame.Channel, member: frame.MemberID, name: frame.Name}
		message.signalUpdated = frame.UpdatedAt
		message.signalExpires = frame.ExpiresAt
	}
	if err := m.queueOutbound(ctx, c, message); err != nil {
		return err
	}
	m.sentMessages.Add(1)
	m.sentBytes.Add(uint64(len(data)))
	return nil
}
