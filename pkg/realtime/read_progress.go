package realtime

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

type ManagedRealtimeReadProgressClient interface {
	GetReadProgress(context.Context, string, string, string, bool) (state.ManagedRealtimeReadProgress, error)
	AdvanceReadProgress(context.Context, string, string, string, bool, int64) (state.ManagedRealtimeReadProgress, error)
}

func (m *Manager) resumeReadProgress(ctx context.Context, c *connection, frame resumeClientFrame, inbox, advance bool) {
	client, ok := m.cfg.HistoryReader.(ManagedRealtimeReadProgressClient)
	if !ok || c.info.Principal == "" {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_error", Channel: frame.Channel, Inbox: inbox, Code: "read_progress_requires_identity"})
		return
	}
	if frame.Sequence < 0 || frame.Data != nil || frame.State != nil || frame.Name != "" || frame.TTLMS != 0 || frame.Consumer != "" || frame.MessageID != "" || frame.Subscription != "" || frame.PresenceScope != "" || frame.DirectMessages || frame.After != 0 || frame.ReadReceipts {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_error", Channel: frame.Channel, Inbox: inbox, Code: "invalid_read_progress"})
		return
	}
	c.mu.Lock()
	allowed := false
	lastSent := int64(0)
	if inbox {
		if c.inboxSub != nil && frame.Channel == "" {
			allowed = true
			lastSent = c.inboxSub.lastSent
		}
	} else {
		if sub := c.resumeSubs[frame.Channel]; sub != nil {
			allowed = true
			lastSent = sub.lastSent
		}
	}
	if allowed {
		c.readReceipts = true
	}
	now := time.Now()
	if c.readWindow.IsZero() || now.Sub(c.readWindow) >= time.Second {
		c.readWindow = now
		c.readCount = 0
	}
	limited := c.readCount >= 20
	if !limited {
		c.readCount++
	}
	c.mu.Unlock()
	if !allowed || (advance && frame.Sequence > lastSent) {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_error", Channel: frame.Channel, Inbox: inbox, Code: "invalid_read_progress"})
		return
	}
	if limited {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_error", Channel: frame.Channel, Inbox: inbox, Code: "read_rate_limited"})
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	defer cancel()
	var progress state.ManagedRealtimeReadProgress
	var err error
	if advance {
		progress, err = client.AdvanceReadProgress(readCtx, c.info.EndpointID, c.info.Principal, frame.Channel, inbox, frame.Sequence)
	} else {
		progress, err = client.GetReadProgress(readCtx, c.info.EndpointID, c.info.Principal, frame.Channel, inbox)
	}
	if err != nil {
		code := "read_progress_unavailable"
		if status.Code(err) == codes.ResourceExhausted {
			code = "read_marker_limit"
		}
		if status.Code(err) == codes.InvalidArgument {
			code = "invalid_read_progress"
		}
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_error", Channel: frame.Channel, Inbox: inbox, Code: code})
		return
	}
	reader, _ := state.ManagedRealtimeReadPrincipalKey(c.info.Principal)
	wire := resumeServerFrame{Type: "read_receipt", Channel: frame.Channel, Inbox: inbox, MemberID: reader, Sequence: progress.Sequence, Unread: progress.Unread, LatestSequence: progress.LatestSequence, OldestSequence: progress.OldestSequence, HistoryUnavailable: progress.HistoryUnavailable, UpdatedAt: progress.UpdatedAt}
	if !advance {
		m.queueResumeControl(ctx, c, wire)
		return
	}
	route := frame.Channel
	if inbox {
		route = reader
		wire.Channel = ""
	}
	ephemeral := EphemeralFrame{Type: "read_receipt", MemberID: reader, Inbox: inbox, Sequence: progress.Sequence, Unread: progress.Unread, LatestSequence: progress.LatestSequence, OldestSequence: progress.OldestSequence, HistoryUnavailable: progress.HistoryUnavailable, UpdatedAt: progress.UpdatedAt}
	if inbox {
		m.broadcastInboxRead(ctx, c.info.EndpointID, ephemeral)
	} else {
		m.broadcastEphemeral(ctx, channelKey{endpointID: c.info.EndpointID, channel: route}, "", wire)
	}
	if err = m.relayFleetEphemeral(ctx, c.info.EndpointID, route, ephemeral); err != nil {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_error", Channel: frame.Channel, Inbox: inbox, Code: "read_relay_unavailable"})
	}
}

// Inbox receipt broadcasts never enter the public channel subscriber directory.
func (m *Manager) broadcastInboxRead(ctx context.Context, ep string, frame EphemeralFrame) {
	m.mu.RLock()
	var recipients []*connection
	for _, c := range m.conns {
		if c.info.EndpointID != ep {
			continue
		}
		key, err := state.ManagedRealtimeReadPrincipalKey(c.info.Principal)
		if err != nil || key != frame.MemberID {
			continue
		}
		c.mu.RLock()
		active := c.inboxSub != nil && c.readReceipts
		c.mu.RUnlock()
		if active {
			recipients = append(recipients, c)
		}
	}
	m.mu.RUnlock()
	for _, c := range recipients {
		m.queueResumeControl(ctx, c, resumeServerFrame{Type: "read_receipt", Inbox: true, MemberID: frame.MemberID, Sequence: frame.Sequence, Unread: frame.Unread, LatestSequence: frame.LatestSequence, OldestSequence: frame.OldestSequence, HistoryUnavailable: frame.HistoryUnavailable, UpdatedAt: frame.UpdatedAt})
	}
}

func validReadReader(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
