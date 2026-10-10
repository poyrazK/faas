package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type realtimeInboxRequest struct {
	Device     json.RawMessage `json:"device,omitempty"`
	Inbox      bool            `json:"inbox,omitempty"`
	Channel    string          `json:"channel,omitempty"`
	Action     string          `json:"action"`
	NodeName   string          `json:"node_name"`
	EndpointID string          `json:"endpoint_id"`
	Principal  string          `json:"principal"`
	Consumer   string          `json:"consumer,omitempty"`
	Sequence   int64           `json:"sequence"`
	Limit      int             `json:"limit,omitempty"`
}

type realtimeInboxResponse struct {
	Preferences  *api.RealtimeNotificationPreferences `json:"preferences,omitempty"`
	ReadProgress *state.ManagedRealtimeReadProgress   `json:"read_progress,omitempty"`
	History      state.ManagedRealtimeChannelHistory  `json:"history"`
	Sequence     int64                                `json:"sequence"`
}

func (c *RealtimeHistoryClientImpl) inboxOperation(ctx context.Context, req realtimeInboxRequest) (realtimeInboxResponse, error) {
	var out realtimeInboxResponse
	if c == nil || c.conn == nil || c.nodeName == "" {
		return out, fmt.Errorf("apidgrpc: realtime inbox requires a connection and node name")
	}
	req.NodeName = c.nodeName
	data, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, "/onebox.faas.apid.v1.RealtimeFleet/InboxOperation", wrapperspb.Bytes(data), response); err != nil {
		return out, fmt.Errorf("apidgrpc: realtime inbox operation: %w", err)
	}
	if err := json.Unmarshal(response.GetValue(), &out); err != nil {
		return out, fmt.Errorf("apidgrpc: decode realtime inbox: %w", err)
	}
	return out, nil
}

func (c *RealtimeHistoryClientImpl) ReadInbox(ctx context.Context, endpointID, principal string, after int64, limit int) (state.ManagedRealtimeChannelHistory, error) {
	response, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "read", EndpointID: endpointID, Principal: principal, Sequence: after, Limit: limit})
	return response.History, err
}

func (c *RealtimeHistoryClientImpl) LoadInboxCursor(ctx context.Context, endpointID, principal, consumer string, initial int64) (int64, error) {
	response, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "load", EndpointID: endpointID, Principal: principal, Consumer: consumer, Sequence: initial})
	return response.Sequence, err
}

func (c *RealtimeHistoryClientImpl) AdvanceInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	response, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "advance", EndpointID: endpointID, Principal: principal, Consumer: consumer, Sequence: sequence})
	return response.Sequence, err
}

func (c *RealtimeHistoryClientImpl) ResetInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	response, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "reset", EndpointID: endpointID, Principal: principal, Consumer: consumer, Sequence: sequence})
	return response.Sequence, err
}

var _ realtime.ManagedRealtimeInboxClient = (*RealtimeHistoryClientImpl)(nil)

func (c *RealtimeHistoryClientImpl) GetReadProgress(ctx context.Context, ep, principal, channel string, inbox bool) (state.ManagedRealtimeReadProgress, error) {
	out, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "read_state", EndpointID: ep, Principal: principal, Channel: channel, Inbox: inbox})
	if err != nil {
		return state.ManagedRealtimeReadProgress{}, err
	}
	if out.ReadProgress == nil {
		return state.ManagedRealtimeReadProgress{}, fmt.Errorf("apidgrpc: missing read progress")
	}
	return *out.ReadProgress, nil
}
func (c *RealtimeHistoryClientImpl) AdvanceReadProgress(ctx context.Context, ep, principal, channel string, inbox bool, seq int64) (state.ManagedRealtimeReadProgress, error) {
	out, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "read_advance", EndpointID: ep, Principal: principal, Channel: channel, Inbox: inbox, Sequence: seq})
	if err != nil {
		return state.ManagedRealtimeReadProgress{}, err
	}
	if out.ReadProgress == nil {
		return state.ManagedRealtimeReadProgress{}, fmt.Errorf("apidgrpc: missing read progress")
	}
	return *out.ReadProgress, nil
}

func (c *RealtimeHistoryClientImpl) RegisterInboxPush(ctx context.Context, ep, principal, device string, data json.RawMessage) error {
	_, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "push_register", EndpointID: ep, Principal: principal, Consumer: device, Device: data})
	return err
}
func (c *RealtimeHistoryClientImpl) UnregisterInboxPush(ctx context.Context, ep, principal, device string) error {
	_, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "push_unregister", EndpointID: ep, Principal: principal, Consumer: device})
	return err
}

var _ realtime.ManagedRealtimePushClient = (*RealtimeHistoryClientImpl)(nil)

func (c *RealtimeHistoryClientImpl) InboxNotificationPreferences(ctx context.Context, ep, principal string, update json.RawMessage) (api.RealtimeNotificationPreferences, error) {
	action := "preferences_get"
	if update != nil {
		action = "preferences_put"
	}
	out, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: action, EndpointID: ep, Principal: principal, Device: update})
	if err != nil {
		return api.RealtimeNotificationPreferences{}, err
	}
	if out.Preferences == nil {
		return api.RealtimeNotificationPreferences{}, fmt.Errorf("apidgrpc: missing notification preferences")
	}
	return *out.Preferences, nil
}

var _ realtime.ManagedRealtimeNotificationPreferencesClient = (*RealtimeHistoryClientImpl)(nil)
