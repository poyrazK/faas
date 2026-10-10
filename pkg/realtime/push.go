package realtime

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ManagedRealtimePushClient interface {
	RegisterInboxPush(context.Context, string, string, string, json.RawMessage) error
	UnregisterInboxPush(context.Context, string, string, string) error
}

func (m *Manager) resumeInboxPush(ctx context.Context, c *connection, frame resumeClientFrame) {
	client, ok := m.cfg.HistoryReader.(ManagedRealtimePushClient)
	reply := resumeServerFrame{Type: "inbox_push_error", Consumer: frame.Consumer, Code: "invalid_push_registration"}
	c.mu.Lock()
	allowed := c.inboxSub != nil && c.inboxSub.consumer == frame.Consumer && c.info.Principal != ""
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
	if !ok || !allowed || limited || frame.Channel != "" || frame.Sequence != 0 || frame.After != 0 || frame.Subscription != "" || frame.MessageID != "" || frame.Name != "" || frame.State != nil || frame.TTLMS != 0 || frame.PresenceScope != "" || frame.DirectMessages || frame.ReadReceipts || len(frame.Data) > 4096 {
		m.queueResumeControl(ctx, c, reply)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	defer cancel()
	var err error
	if frame.Type == "inbox_push_register" && len(frame.Data) > 0 {
		err = client.RegisterInboxPush(callCtx, c.info.EndpointID, c.info.Principal, frame.Consumer, frame.Data)
	} else if frame.Type == "inbox_push_unregister" && frame.Data == nil {
		err = client.UnregisterInboxPush(callCtx, c.info.EndpointID, c.info.Principal, frame.Consumer)
	} else {
		m.queueResumeControl(ctx, c, reply)
		return
	}
	if err == nil {
		reply.Type = "inbox_push_registered"
		if frame.Type == "inbox_push_unregister" {
			reply.Type = "inbox_push_unregistered"
		}
		reply.Code = ""
	} else {
		reply.Code = "push_unavailable"
		if status.Code(err) == codes.ResourceExhausted {
			reply.Code = "push_device_limit"
		}
		if status.Code(err) == codes.FailedPrecondition {
			reply.Code = "push_provider_unavailable"
		}
		if status.Code(err) == codes.InvalidArgument {
			reply.Code = "invalid_push_registration"
		}
	}
	m.queueResumeControl(ctx, c, reply)
}

type ManagedRealtimeNotificationPreferencesClient interface {
	InboxNotificationPreferences(context.Context, string, string, json.RawMessage) (api.RealtimeNotificationPreferences, error)
}

func (m *Manager) resumeInboxNotificationPreferences(ctx context.Context, c *connection, frame resumeClientFrame) {
	reply := resumeServerFrame{Type: "inbox_preferences_error", Consumer: frame.Consumer, Code: "invalid_notification_preferences"}
	client, ok := m.cfg.HistoryReader.(ManagedRealtimeNotificationPreferencesClient)
	c.mu.Lock()
	allowed := c.inboxSub != nil && c.inboxSub.consumer == frame.Consumer && c.info.Principal != ""
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
	if !ok || !allowed || limited || frame.Channel != "" || frame.Sequence != 0 || frame.After != 0 || frame.Subscription != "" || frame.MessageID != "" || frame.Name != "" || frame.State != nil || frame.TTLMS != 0 || frame.PresenceScope != "" || frame.DirectMessages || frame.ReadReceipts || len(frame.Data) > 4096 {
		m.queueResumeControl(ctx, c, reply)
		return
	}
	if frame.Type == "inbox_preferences_get" && frame.Data != nil || frame.Type == "inbox_preferences_put" && len(frame.Data) == 0 {
		m.queueResumeControl(ctx, c, reply)
		return
	}
	call, cancel := context.WithTimeout(ctx, resumeHistoryReadTimeout)
	defer cancel()
	p, err := client.InboxNotificationPreferences(call, c.info.EndpointID, c.info.Principal, frame.Data)
	if err == nil {
		reply.Type = "inbox_preferences"
		reply.Code = ""
		reply.Preferences = &p
	} else {
		reply.Code = "notification_preferences_unavailable"
		if status.Code(err) == codes.InvalidArgument {
			reply.Code = "invalid_notification_preferences"
		}
		if status.Code(err) == codes.ResourceExhausted {
			reply.Code = "notification_preferences_limit"
		}
	}
	m.queueResumeControl(ctx, c, reply)
}
