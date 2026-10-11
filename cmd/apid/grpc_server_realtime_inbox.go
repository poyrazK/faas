package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// Inbox reads are separate from channel reads. The realtime node supplies the
// principal verified during the handshake; the client never chooses it.
func (r *realtimeFleetReceiver) InboxOperation(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.inbox == nil || r.store == nil {
		return nil, status.Error(codes.Unavailable, "realtime inbox unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > realtimeFleetRequestMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime inbox request")
	}
	var req realtimeInboxRequest
	if decodeRealtimeFleetJSON(envelope.GetValue(), &req) != nil || (req.Action != "channel_read" && api.ValidateRealtimePrincipal(req.Principal) != nil) || req.Sequence < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime inbox request")
	}
	if err := r.requireEnabledEndpoint(ctx, req.EndpointID); err != nil {
		return nil, err
	}
	node, err := r.lookupNode(ctx, req.NodeName)
	if err != nil {
		return nil, err
	}
	if !node.Active {
		return nil, status.Error(codes.FailedPrecondition, "realtime node inactive")
	}
	var response realtimeInboxResponse
	switch req.Action {
	case "preferences_get", "preferences_put":
		preferences, ok := r.store.(state.ManagedRealtimePushPreferencesStore)
		if !ok {
			return nil, status.Error(codes.Unavailable, "notification preferences unavailable")
		}
		if req.Action == "preferences_put" {
			p, decodeErr := api.DecodeRealtimeNotificationPreferences(req.Device)
			if decodeErr != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid notification preferences")
			}
			err = preferences.PutManagedRealtimeNotificationPreferences(ctx, req.EndpointID, req.Principal, p)
			if err == nil {
				response.Preferences = &p
			}
		} else {
			var p api.RealtimeNotificationPreferences
			p, err = preferences.GetManagedRealtimeNotificationPreferences(ctx, req.EndpointID, req.Principal)
			response.Preferences = &p
		}

	case "push_register", "push_unregister":
		push, ok := r.store.(state.ManagedRealtimePushStore)
		if !ok {
			return nil, status.Error(codes.Unavailable, "push unavailable")
		}
		if req.Action == "push_unregister" {
			err = push.DeleteManagedRealtimePushDevice(ctx, req.EndpointID, req.Principal, req.Consumer)
		} else {
			var registration realtimePushRegistration
			if decodeRealtimeFleetJSON(req.Device, &registration) != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid push registration")
			}
			err = registerRealtimePush(ctx, push, req.EndpointID, req.Principal, req.Consumer, registration)
		}

	case "read_state", "read_advance":
		reads, ok := r.store.(state.ManagedRealtimeReadProgressStore)
		if !ok {
			return nil, status.Error(codes.Unavailable, "read progress unavailable")
		}
		var progress state.ManagedRealtimeReadProgress
		if req.Action == "read_state" {
			progress, err = reads.GetManagedRealtimeReadProgress(ctx, req.EndpointID, req.Principal, req.Channel, req.Inbox)
		} else {
			progress, err = reads.AdvanceManagedRealtimeReadProgress(ctx, req.EndpointID, req.Principal, req.Channel, req.Inbox, req.Sequence)
		}
		response.ReadProgress = &progress

	case "channel_read":
		if history, ok := r.store.(state.ManagedRealtimeHistoryStore); ok {
			response.History, err = history.ReadManagedRealtimeChannelHistory(ctx, req.EndpointID, req.Channel, req.Sequence, req.Limit)
		} else {
			return nil, status.Error(codes.Unavailable, "realtime history unavailable")
		}
	case "read":
		response.History, err = r.inbox.ReadManagedRealtimeInbox(ctx, req.EndpointID, req.Principal, req.Sequence, req.Limit)
	case "load":
		response.Sequence, err = r.inbox.LoadManagedRealtimeInboxCursor(ctx, req.EndpointID, req.Principal, req.Consumer, req.Sequence)
	case "advance":
		response.Sequence, err = r.inbox.AdvanceManagedRealtimeInboxCursor(ctx, req.EndpointID, req.Principal, req.Consumer, req.Sequence)
	case "reset":
		response.Sequence, err = r.inbox.ResetManagedRealtimeInboxCursor(ctx, req.EndpointID, req.Principal, req.Consumer, req.Sequence)
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid realtime inbox operation")
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrManagedRealtimeHistoryInvalid), errors.Is(err, state.ErrManagedRealtimeDurableCursorInvalid):
			return nil, status.Error(codes.InvalidArgument, "invalid realtime inbox request")
		case errors.Is(err, state.ErrManagedRealtimeFallbackSubscription):
			return nil, status.Error(codes.FailedPrecondition, "push provider unavailable")
		case errors.Is(err, state.ErrManagedRealtimeDurableCursorExpired):
			return nil, status.Error(codes.FailedPrecondition, "realtime inbox cursor expired")
		case errors.Is(err, state.ErrManagedRealtimeHistoryLimit), errors.Is(err, state.ErrManagedRealtimeDurableCursorLimit):
			return nil, status.Error(codes.ResourceExhausted, "realtime inbox limit reached")
		case errors.Is(err, state.ErrNotFound):
			return nil, status.Error(codes.NotFound, "realtime inbox checkpoint missing")
		default:
			return nil, status.Error(codes.Unavailable, "realtime inbox storage unavailable")
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode realtime inbox result")
	}
	return wrapperspb.Bytes(encoded), nil
}
