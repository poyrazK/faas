package realtime

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ManagedRealtimeInboxClient reads only the verified principal's stream and
// persists a separate checkpoint for each explicitly named device.
type ManagedRealtimeInboxClient interface {
	ReadInbox(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error)
	LoadInboxCursor(context.Context, string, string, string, int64) (int64, error)
	AdvanceInboxCursor(context.Context, string, string, string, int64) (int64, error)
	ResetInboxCursor(context.Context, string, string, string, int64) (int64, error)
}

const maxInboxUnacknowledged = 16

type inboxSubscription struct {
	cancel    context.CancelFunc
	wake      chan struct{}
	consumer  string
	lastSent  int64
	lastAck   int64
	resyncing bool
}

func (m *Manager) inboxError(ctx context.Context, c *connection, consumer, code string) {
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "inbox_error", Consumer: consumer, Code: code})
}

func (m *Manager) inboxResync(ctx context.Context, c *connection, consumer string, page state.ManagedRealtimeChannelHistory) {
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "inbox_resync_required", Consumer: consumer,
		OldestSequence: page.OldestSequence, LatestSequence: page.LatestSequence})
}

func (m *Manager) resumeInboxSubscribe(ctx context.Context, c *connection, frame resumeClientFrame) {
	client, ok := m.cfg.HistoryReader.(ManagedRealtimeInboxClient)
	if !ok || c.info.Principal == "" {
		m.inboxError(ctx, c, frame.Consumer, "inbox_unavailable")
		return
	}
	if !validDurableSubscription(frame.Consumer) || frame.After < 0 || frame.Sequence != 0 || frame.Channel != "" {
		m.inboxError(ctx, c, frame.Consumer, "invalid_subscription")
		return
	}
	c.mu.RLock()
	occupied := c.inboxSub != nil || len(c.resumeSubs) >= maxResumePerConnection
	c.mu.RUnlock()
	if occupied || !m.reserveResume() {
		m.inboxError(ctx, c, frame.Consumer, "subscription_limit")
		return
	}
	release := true
	defer func() {
		if release {
			m.resumeCount.Add(-1)
		}
	}()
	readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	after, err := client.LoadInboxCursor(readCtx, c.info.EndpointID, c.info.Principal, frame.Consumer, frame.After)
	cancel()
	if err != nil {
		m.inboxError(ctx, c, frame.Consumer, durableCursorErrorCode(err))
		return
	}
	readCtx, cancel = context.WithTimeout(ctx, resumeHistoryReadTimeout)
	page, err := client.ReadInbox(readCtx, c.info.EndpointID, c.info.Principal, after, resumeHistoryPageSize)
	cancel()
	if err != nil {
		m.inboxError(ctx, c, frame.Consumer, "history_read_failed")
		return
	}
	if page.HistoryUnavailable {
		m.inboxResync(ctx, c, frame.Consumer, page)
		return
	}
	subCtx, subCancel := context.WithCancel(ctx)
	sub := &inboxSubscription{cancel: subCancel, wake: make(chan struct{}, 1), consumer: frame.Consumer, lastSent: after, lastAck: after}
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		subCancel()
		return
	default:
	}
	if c.inboxSub != nil || len(c.resumeSubs) >= maxResumePerConnection {
		c.mu.Unlock()
		subCancel()
		m.inboxError(ctx, c, frame.Consumer, "subscription_limit")
		return
	}
	c.inboxSub = sub
	if frame.ReadReceipts {
		c.readReceipts = true
	}
	if frame.DirectMessages {
		c.directMessages = true
	}
	c.mu.Unlock()
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "inbox_subscribed", Consumer: frame.Consumer, Sequence: after})
	release = false
	go m.pumpInbox(subCtx, c, client, sub, page)
}

func (m *Manager) pumpInbox(ctx context.Context, c *connection, client ManagedRealtimeInboxClient, sub *inboxSubscription, page state.ManagedRealtimeChannelHistory) {
	defer m.resumeCount.Add(-1)
	defer func() {
		sub.cancel()
		c.mu.Lock()
		if c.inboxSub == sub && !sub.resyncing {
			c.inboxSub = nil
		}
		c.mu.Unlock()
	}()
	ticker := time.NewTicker(m.cfg.ResumePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if page.HistoryUnavailable {
			c.mu.Lock()
			sub.resyncing = true
			c.mu.Unlock()
			m.inboxResync(ctx, c, sub.consumer, page)
			return
		}
		for _, message := range page.Messages {
			c.mu.Lock()
			if c.inboxSub != sub || ctx.Err() != nil {
				c.mu.Unlock()
				return
			}
			if sub.lastSent-sub.lastAck >= maxInboxUnacknowledged {
				c.mu.Unlock()
				break
			}
			if message.Sequence != sub.lastSent+1 {
				sub.resyncing = true
				c.mu.Unlock()
				m.inboxResync(ctx, c, sub.consumer, page)
				return
			}
			err := m.queueResume(ctx, c, resumeServerFrame{Type: "inbox_message", Consumer: sub.consumer,
				Sequence: message.Sequence, MessageID: message.TargetMessageID, TargetMessageID: message.TargetMessageID, Version: message.Version, MessageEvent: message.MessageEvent, Deleted: message.Deleted, DataBase64: base64.StdEncoding.EncodeToString(message.Data), Binary: message.Binary})
			if err != nil {
				c.mu.Unlock()
				_ = c.close(websocket.CloseTryAgainLater, resumeOutputQueueFullReason)
				return
			}
			sub.lastSent = message.Sequence
			c.mu.Unlock()
		}
		c.mu.RLock()
		lastSent, blocked := sub.lastSent, sub.lastSent-sub.lastAck >= maxInboxUnacknowledged
		c.mu.RUnlock()
		if lastSent >= page.LatestSequence || blocked {
			select {
			case <-ticker.C:
			case <-sub.wake:
			case <-ctx.Done():
				return
			case <-c.done:
				return
			}
		}
		readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
		next, err := client.ReadInbox(readCtx, c.info.EndpointID, c.info.Principal, lastSent, resumeHistoryPageSize)
		cancel()
		if err != nil {
			m.inboxError(ctx, c, sub.consumer, "history_read_failed")
			_ = c.close(websocket.CloseTryAgainLater, "inbox history unavailable")
			return
		}
		if len(next.Messages) == 0 && lastSent < next.LatestSequence && !next.HistoryUnavailable {
			c.mu.Lock()
			sub.resyncing = true
			c.mu.Unlock()
			m.inboxResync(ctx, c, sub.consumer, next)
			return
		}
		page = next
	}
}

func (m *Manager) resumeInboxAck(ctx context.Context, c *connection, frame resumeClientFrame) {
	c.mu.RLock()
	sub := c.inboxSub
	valid := sub != nil && frame.Consumer == sub.consumer && frame.Sequence >= sub.lastAck && frame.Sequence <= sub.lastSent && frame.After == 0 && frame.Channel == ""
	c.mu.RUnlock()
	if !valid {
		m.inboxError(ctx, c, frame.Consumer, "invalid_ack")
		return
	}
	client, ok := m.cfg.HistoryReader.(ManagedRealtimeInboxClient)
	if !ok {
		m.inboxError(ctx, c, frame.Consumer, "inbox_unavailable")
		return
	}
	ackCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	_, err := client.AdvanceInboxCursor(ackCtx, c.info.EndpointID, c.info.Principal, sub.consumer, frame.Sequence)
	cancel()
	if err != nil {
		m.inboxError(ctx, c, sub.consumer, "checkpoint_write_failed")
		_ = c.close(websocket.CloseTryAgainLater, "inbox checkpoint unavailable")
		return
	}
	c.mu.Lock()
	if c.inboxSub != sub {
		c.mu.Unlock()
		return
	}
	if frame.Sequence > sub.lastAck {
		sub.lastAck = frame.Sequence
	}
	c.mu.Unlock()
	select {
	case sub.wake <- struct{}{}:
	default:
	}
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "inbox_acknowledged", Consumer: sub.consumer, Sequence: frame.Sequence})
}

func (m *Manager) resumeInboxReset(ctx context.Context, c *connection, frame resumeClientFrame) {
	client, ok := m.cfg.HistoryReader.(ManagedRealtimeInboxClient)
	if !ok || c.info.Principal == "" || !validDurableSubscription(frame.Consumer) || frame.Sequence < 0 || frame.After != 0 || frame.Channel != "" {
		m.inboxError(ctx, c, frame.Consumer, "invalid_cursor_reset")
		return
	}
	c.mu.Lock()
	sub := c.inboxSub
	if sub != nil && (sub.consumer != frame.Consumer || !sub.resyncing) {
		c.mu.Unlock()
		m.inboxError(ctx, c, frame.Consumer, "cursor_reset_not_allowed")
		return
	}
	if sub != nil {
		sub.cancel()
		c.inboxSub = nil
	}
	c.mu.Unlock()
	resetCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	_, err := client.ResetInboxCursor(resetCtx, c.info.EndpointID, c.info.Principal, frame.Consumer, frame.Sequence)
	cancel()
	if err != nil {
		if code := status.Code(err); code == codes.FailedPrecondition || code == codes.InvalidArgument {
			readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
			page, readErr := client.ReadInbox(readCtx, c.info.EndpointID, c.info.Principal, 0, 1)
			cancel()
			if readErr == nil {
				m.inboxResync(ctx, c, frame.Consumer, page)
				return
			}
		}
		if status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded {
			m.inboxError(ctx, c, frame.Consumer, "checkpoint_write_failed")
			_ = c.close(websocket.CloseTryAgainLater, "inbox checkpoint unavailable")
			return
		}
		m.inboxError(ctx, c, frame.Consumer, "cursor_reset_failed")
		return
	}
	m.queueResumeControl(ctx, c, resumeServerFrame{Type: "inbox_reset", Consumer: frame.Consumer, Sequence: frame.Sequence})
}

// WakePrincipalInbox nudges active inbox pumps after a committed append.
// Polling remains the recovery path when the notification or a node is lost.
func (m *Manager) WakePrincipalInbox(endpointID, principal string) (PrincipalSendStatus, error) {
	value, ok := m.endpoints.Load(endpointID)
	if !ok || value.(*endpointState).revoked.Load() {
		return PrincipalSendStatus{}, ErrEndpointNotFound
	}
	result := PrincipalSendStatus{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.conns {
		if c.info.EndpointID != endpointID || c.info.Principal != principal {
			continue
		}
		c.mu.RLock()
		sub := c.inboxSub
		if sub != nil && !sub.resyncing {
			result.Recipients++
			select {
			case sub.wake <- struct{}{}:
			default:
			}
		}
		c.mu.RUnlock()
	}
	return result, nil
}
