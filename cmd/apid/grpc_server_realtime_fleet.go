package main

// RealtimeFleet is a private control-plane service for short-lived presence
// leases and best-effort ephemeral delivery. Its bounded JSON payloads use the
// protobuf BytesValue wrapper so no public message or durable history API is
// involved.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	realtimeFleetRequestMaxBytes = 8 << 10
	realtimeFleetStateMaxBytes   = state.ManagedRealtimePresenceStateMaxBytes
	realtimeFleetSignalMaxBytes  = 2 << 10
	realtimeFleetReceiptMaxBytes = 1 << 20
)

type realtimeFleetNodeStore interface {
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
}

type realtimeEphemeralRouter interface {
	RelayEphemeral(context.Context, string, string, string, realtime.EphemeralFrame) error
}

type realtimeFleetReceiver struct {
	store    realtimeHistoryReaderStore
	nodes    realtimeFleetNodeStore
	presence state.ManagedRealtimePresenceStore
	receipts state.ManagedRealtimeDirectMessageReceiptStore
	inbox    state.ManagedRealtimeInboxStore
	router   realtimeEphemeralRouter
}

type realtimeFleetPresenceRequest struct {
	Action       string          `json:"action"`
	NodeName     string          `json:"node_name,omitempty"`
	EndpointID   string          `json:"endpoint_id"`
	Channel      string          `json:"channel"`
	ConnectionID string          `json:"connection_id,omitempty"`
	MemberID     string          `json:"member_id,omitempty"`
	Principal    string          `json:"principal,omitempty"`
	State        json.RawMessage `json:"state,omitempty"`
}

type realtimeFleetPresenceResponse struct {
	Members     []realtime.PresenceMember `json:"members"`
	OwnMemberID string                    `json:"own_member_id,omitempty"`
	ObservedAt  *time.Time                `json:"observed_at,omitempty"`
}

type realtimeFleetRelayRequest struct {
	NodeName   string                  `json:"node_name"`
	EndpointID string                  `json:"endpoint_id"`
	Channel    string                  `json:"channel"`
	Frame      realtime.EphemeralFrame `json:"frame"`
}

type realtimeFleetDirectMessageTargetRequest struct {
	ConnectionID string `json:"connection_id"`
	AckSupported bool   `json:"ack_supported"`
}

type realtimeFleetDirectMessageResultRequest struct {
	ConnectionID string `json:"connection_id"`
	QueueStatus  string `json:"queue_status"`
}

type realtimeFleetDirectMessageRequest struct {
	Action       string                                    `json:"action"`
	NodeName     string                                    `json:"node_name"`
	EndpointID   string                                    `json:"endpoint_id"`
	MessageID    string                                    `json:"message_id"`
	ConnectionID string                                    `json:"connection_id,omitempty"`
	Targets      []realtimeFleetDirectMessageTargetRequest `json:"targets,omitempty"`
	Results      []realtimeFleetDirectMessageResultRequest `json:"results,omitempty"`
}

type realtimeFleetServer interface {
	InboxOperation(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
	PresenceOperation(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
	RelayEphemeral(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
	DirectMessageOperation(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}

func (r *realtimeFleetReceiver) PresenceOperation(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.presence == nil || r.store == nil {
		return nil, status.Error(codes.Unavailable, "realtime presence store unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > realtimeFleetRequestMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime presence request")
	}
	var req realtimeFleetPresenceRequest
	if err := decodeRealtimeFleetJSON(envelope.GetValue(), &req); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime presence request")
	}
	if req.EndpointID == "" || !realtime.ValidateChannel(req.Channel) {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime presence request")
	}
	var members []state.ManagedRealtimePresenceLease
	var ownMemberID string
	var observedAt time.Time
	switch req.Action {
	case "upsert":
		if err := r.requireEnabledEndpoint(ctx, req.EndpointID); err != nil {
			return nil, err
		}
		if req.ConnectionID == "" || req.MemberID == "" || len(req.State) == 0 || len(req.State) > realtimeFleetStateMaxBytes || !validJSONPresenceObject(req.State) {
			return nil, status.Error(codes.InvalidArgument, "invalid realtime presence lease")
		}
		node, err := r.lookupNode(ctx, req.NodeName)
		if err != nil {
			return nil, err
		}
		if !node.Active {
			return nil, status.Error(codes.FailedPrecondition, "realtime node inactive")
		}
		members, err = r.presence.UpsertManagedRealtimePresenceLease(ctx, state.ManagedRealtimePresenceLease{
			EndpointID: req.EndpointID, Channel: req.Channel, NodeID: node.ID,
			ConnectionID: req.ConnectionID, MemberID: req.MemberID,
			Principal: req.Principal, State: append([]byte(nil), req.State...),
			ExpiresAt: time.Now().UTC().Add(state.ManagedRealtimePresenceLeaseTTL),
		})
		if err != nil {
			return nil, realtimePresenceStoreStatus(err)
		}
		for _, member := range members {
			if (req.Principal != "" && member.Principal == req.Principal) || (req.Principal == "" && member.MemberID == req.MemberID) {
				ownMemberID = member.MemberID
				break
			}
		}
		if ownMemberID == "" {
			return nil, status.Error(codes.Internal, "realtime presence upsert omitted the member")
		}
	case "delete":
		if req.ConnectionID == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid realtime presence lease")
		}
		node, err := r.lookupNode(ctx, req.NodeName)
		if errors.Is(err, state.ErrNotFound) {
			members, err = r.presence.ReadManagedRealtimePresenceSnapshot(ctx, req.EndpointID, req.Channel)
			observedAt = time.Now().UTC()
		} else if err != nil {
			return nil, err
		} else {
			members, observedAt, err = r.presence.DeleteManagedRealtimePresenceLease(ctx, req.EndpointID, req.Channel, node.ID, req.ConnectionID)
		}
		if err != nil {
			return nil, realtimePresenceStoreStatus(err)
		}
	case "snapshot":
		if err := r.requireEnabledEndpoint(ctx, req.EndpointID); err != nil {
			return nil, err
		}
		var err error
		members, err = r.presence.ReadManagedRealtimePresenceSnapshot(ctx, req.EndpointID, req.Channel)
		if err != nil {
			return nil, realtimePresenceStoreStatus(err)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid realtime presence operation")
	}

	response := realtimeFleetPresenceResponse{Members: make([]realtime.PresenceMember, 0, len(members)), OwnMemberID: ownMemberID}
	if !observedAt.IsZero() {
		response.ObservedAt = &observedAt
	}
	for _, member := range members {
		response.Members = append(response.Members, realtime.PresenceMember{
			MemberID: member.MemberID, State: append(json.RawMessage(nil), member.State...),
			ConnectionCount: member.ConnectionCount, UpdatedAt: member.UpdatedAt,
		})
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode realtime presence snapshot")
	}
	return wrapperspb.Bytes(data), nil
}

func (r *realtimeFleetReceiver) RelayEphemeral(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.router == nil || r.store == nil {
		return nil, status.Error(codes.Unavailable, "realtime fleet relay unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > realtimeFleetRequestMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime ephemeral event")
	}
	var req realtimeFleetRelayRequest
	if err := decodeRealtimeFleetJSON(envelope.GetValue(), &req); err != nil || req.EndpointID == "" || !realtime.ValidateChannel(req.Channel) || !validEphemeralFrame(req.Frame) {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime ephemeral event")
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
	if err := r.router.RelayEphemeral(ctx, node.ID, req.EndpointID, req.Channel, req.Frame); err != nil {
		return nil, status.Error(codes.Unavailable, "realtime ephemeral relay failed")
	}
	return wrapperspb.Bytes([]byte(`{}`)), nil
}

func (r *realtimeFleetReceiver) DirectMessageOperation(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.receipts == nil || r.store == nil || r.nodes == nil {
		return nil, status.Error(codes.Unavailable, "realtime direct message receipts unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > realtimeFleetReceiptMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime direct message request")
	}
	var req realtimeFleetDirectMessageRequest
	if err := decodeRealtimeFleetJSON(envelope.GetValue(), &req); err != nil || req.EndpointID == "" || !validManagedRealtimeFleetMessageID(req.MessageID) {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime direct message request")
	}
	node, err := r.lookupNode(ctx, req.NodeName)
	if err != nil {
		return nil, err
	}
	if !node.Active {
		return nil, status.Error(codes.FailedPrecondition, "realtime node inactive")
	}
	switch req.Action {
	case "register":
		if err := r.requireEnabledEndpoint(ctx, req.EndpointID); err != nil {
			return nil, err
		}
		if len(req.Targets) > state.ManagedRealtimeDirectMessageMaxTargets || len(req.Results) != 0 || req.ConnectionID != "" {
			return nil, status.Error(codes.InvalidArgument, "invalid realtime direct message targets")
		}
		targets := make([]state.ManagedRealtimeDirectMessageTarget, len(req.Targets))
		for i, target := range req.Targets {
			targets[i] = state.ManagedRealtimeDirectMessageTarget{ConnectionID: target.ConnectionID, AckSupported: target.AckSupported}
		}
		created, err := r.receipts.RegisterManagedRealtimeDirectMessageTargets(ctx, req.EndpointID, req.MessageID, node.ID, targets)
		if err != nil {
			return nil, realtimeDirectReceiptStatus(err)
		}
		encoded, err := json.Marshal(struct {
			CreatedConnectionIDs []string `json:"created_connection_ids"`
		}{CreatedConnectionIDs: created})
		if err != nil {
			return nil, status.Error(codes.Internal, "encode realtime direct message result")
		}
		return wrapperspb.Bytes(encoded), nil
	case "update":
		if len(req.Results) > state.ManagedRealtimeDirectMessageMaxTargets || len(req.Targets) != 0 || req.ConnectionID != "" {
			return nil, status.Error(codes.InvalidArgument, "invalid realtime direct message result")
		}
		results := make([]state.ManagedRealtimeDirectMessageDeliveryResult, len(req.Results))
		for i, result := range req.Results {
			results[i] = state.ManagedRealtimeDirectMessageDeliveryResult{ConnectionID: result.ConnectionID, QueueStatus: result.QueueStatus}
		}
		if err := r.receipts.UpdateManagedRealtimeDirectMessageDeliveries(ctx, req.EndpointID, req.MessageID, node.ID, results); err != nil {
			return nil, realtimeDirectReceiptStatus(err)
		}
		return wrapperspb.Bytes([]byte(`{}`)), nil
	case "acknowledge":
		if req.ConnectionID == "" || len(req.Targets) != 0 || len(req.Results) != 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid realtime direct message acknowledgement")
		}
		if err := r.receipts.AcknowledgeManagedRealtimeDirectMessage(ctx, req.EndpointID, req.MessageID, node.ID, req.ConnectionID); err != nil {
			return nil, realtimeDirectReceiptStatus(err)
		}
		return wrapperspb.Bytes([]byte(`{}`)), nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid realtime direct message operation")
	}
}

func validManagedRealtimeFleetMessageID(messageID string) bool {
	if messageID == "" || len(messageID) > 128 || strings.TrimSpace(messageID) != messageID {
		return false
	}
	for _, char := range messageID {
		if char <= 0x20 || char > 0x7e || strings.ContainsRune("/?#", char) {
			return false
		}
	}
	return true
}

func realtimeDirectReceiptStatus(err error) error {
	switch {
	case errors.Is(err, state.ErrManagedRealtimeDirectMessageInvalid):
		return status.Error(codes.InvalidArgument, "invalid realtime direct message receipt")
	case errors.Is(err, state.ErrManagedRealtimeDirectMessageConflict):
		return status.Error(codes.AlreadyExists, "realtime direct message ID conflict")
	case errors.Is(err, state.ErrManagedRealtimeDirectMessageLimit):
		return status.Error(codes.ResourceExhausted, "realtime direct message receipt limit reached")
	case errors.Is(err, state.ErrNotFound):
		return status.Error(codes.NotFound, "realtime direct message receipt not found")
	default:
		return status.Error(codes.Unavailable, "realtime direct message receipt store unavailable")
	}
}

func (r *realtimeFleetReceiver) requireEnabledEndpoint(ctx context.Context, endpointID string) error {
	endpoint, err := r.store.ManagedRealtimeEndpointByID(ctx, endpointID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return status.Error(codes.NotFound, "realtime endpoint not found")
	case err != nil:
		return status.Error(codes.Unavailable, "realtime endpoint lookup unavailable")
	case !endpoint.Enabled:
		return status.Error(codes.FailedPrecondition, "realtime endpoint disabled")
	default:
		return nil
	}
}

func (r *realtimeFleetReceiver) lookupNode(ctx context.Context, name string) (state.ComputeNode, error) {
	if r.nodes == nil || name == "" || strings.TrimSpace(name) != name {
		return state.ComputeNode{}, status.Error(codes.InvalidArgument, "invalid realtime node name")
	}
	node, err := r.nodes.ComputeNodeByName(ctx, name)
	if errors.Is(err, state.ErrNotFound) {
		return state.ComputeNode{}, status.Error(codes.NotFound, "realtime node not found")
	}
	if err != nil {
		return state.ComputeNode{}, status.Error(codes.Unavailable, "realtime node lookup unavailable")
	}
	return node, nil
}

func validJSONPresenceObject(data []byte) bool {
	var object map[string]json.RawMessage
	return json.Valid(data) && json.Unmarshal(data, &object) == nil && object != nil
}

func validEphemeralFrame(frame realtime.EphemeralFrame) bool {
	return realtime.ValidateEphemeralFrame(frame)
}

func realtimePresenceStoreStatus(err error) error {
	switch {
	case errors.Is(err, state.ErrManagedRealtimePresenceInvalid), errors.Is(err, state.ErrManagedRealtimeHistoryInvalid):
		return status.Error(codes.InvalidArgument, "invalid realtime presence lease")
	case errors.Is(err, state.ErrManagedRealtimePresenceLimit):
		return status.Error(codes.ResourceExhausted, "realtime channel presence limit reached")
	case errors.Is(err, state.ErrNotFound):
		return status.Error(codes.NotFound, "realtime endpoint or node not found")
	default:
		return status.Error(codes.Unavailable, "realtime presence store unavailable")
	}
}

func decodeRealtimeFleetJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing realtime fleet JSON")
	}
	return nil
}

func realtimeFleetUnaryHandler(methodName string, method func(realtimeFleetServer, context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)) grpc.MethodDesc {
	return grpc.MethodDesc{MethodName: methodName, Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := new(wrapperspb.BytesValue)
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return method(srv.(realtimeFleetServer), ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/onebox.faas.apid.v1.RealtimeFleet/" + methodName}
		handler := func(ctx context.Context, request any) (any, error) {
			return method(srv.(realtimeFleetServer), ctx, request.(*wrapperspb.BytesValue))
		}
		return interceptor(ctx, in, info, handler)
	}}
}

var realtimeFleetServiceDesc = grpc.ServiceDesc{
	ServiceName: "onebox.faas.apid.v1.RealtimeFleet",
	HandlerType: (*realtimeFleetServer)(nil),
	Methods: []grpc.MethodDesc{
		realtimeFleetUnaryHandler("PresenceOperation", func(server realtimeFleetServer, ctx context.Context, req *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
			return server.PresenceOperation(ctx, req)
		}),
		realtimeFleetUnaryHandler("RelayEphemeral", func(server realtimeFleetServer, ctx context.Context, req *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
			return server.RelayEphemeral(ctx, req)
		}),
		realtimeFleetUnaryHandler("InboxOperation", func(server realtimeFleetServer, ctx context.Context, req *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
			return server.InboxOperation(ctx, req)
		}),
		realtimeFleetUnaryHandler("DirectMessageOperation", func(server realtimeFleetServer, ctx context.Context, req *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
			return server.DirectMessageOperation(ctx, req)
		}),
	},
}

func registerRealtimeFleetReceiver(server *grpc.Server, store state.Store, owner any) {
	if server == nil {
		return
	}
	reader, _ := store.(realtimeHistoryReaderStore)
	nodes, _ := store.(realtimeFleetNodeStore)
	presence, _ := store.(state.ManagedRealtimePresenceStore)
	receipts, _ := store.(state.ManagedRealtimeDirectMessageReceiptStore)
	inbox, _ := store.(state.ManagedRealtimeInboxStore)
	router, _ := owner.(realtimeEphemeralRouter)
	server.RegisterService(&realtimeFleetServiceDesc, &realtimeFleetReceiver{store: reader, nodes: nodes, presence: presence, receipts: receipts, inbox: inbox, router: router})
}
