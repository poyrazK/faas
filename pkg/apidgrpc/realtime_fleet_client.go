package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	realtimeFleetPresenceMethod = "/onebox.faas.apid.v1.RealtimeFleet/PresenceOperation"
	realtimeFleetRelayMethod    = "/onebox.faas.apid.v1.RealtimeFleet/RelayEphemeral"
	realtimeFleetDirectMethod   = "/onebox.faas.apid.v1.RealtimeFleet/DirectMessageOperation"
)

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

func (c *RealtimeHistoryClientImpl) UpsertPresence(ctx context.Context, endpointID, channel, connectionID, memberID, principal string, state json.RawMessage) (string, []realtime.PresenceMember, error) {
	request := realtimeFleetPresenceRequest{
		Action: "upsert", NodeName: c.nodeName, EndpointID: endpointID, Channel: channel,
		ConnectionID: connectionID, MemberID: memberID, Principal: principal, State: append(json.RawMessage(nil), state...),
	}
	ownMemberID, members, _, err := c.presenceOperation(ctx, request)
	return ownMemberID, members, err
}

func (c *RealtimeHistoryClientImpl) DeletePresence(ctx context.Context, endpointID, channel, connectionID string) ([]realtime.PresenceMember, time.Time, error) {
	request := realtimeFleetPresenceRequest{
		Action: "delete", NodeName: c.nodeName, EndpointID: endpointID, Channel: channel, ConnectionID: connectionID,
	}
	_, members, observedAt, err := c.presenceOperation(ctx, request)
	return members, observedAt, err
}

func (c *RealtimeHistoryClientImpl) ReadPresenceSnapshot(ctx context.Context, endpointID, channel string) ([]realtime.PresenceMember, error) {
	_, members, _, err := c.presenceOperation(ctx, realtimeFleetPresenceRequest{Action: "snapshot", EndpointID: endpointID, Channel: channel})
	return members, err
}

func (c *RealtimeHistoryClientImpl) presenceOperation(ctx context.Context, request realtimeFleetPresenceRequest) (string, []realtime.PresenceMember, time.Time, error) {
	if c == nil || c.conn == nil {
		return "", nil, time.Time{}, fmt.Errorf("apidgrpc: realtime fleet client is unavailable")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("apidgrpc: encode realtime presence request: %w", err)
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, realtimeFleetPresenceMethod, wrapperspb.Bytes(data), response); err != nil {
		return "", nil, time.Time{}, fmt.Errorf("apidgrpc: realtime presence operation: %w", err)
	}
	var decoded realtimeFleetPresenceResponse
	if err := json.Unmarshal(response.GetValue(), &decoded); err != nil {
		return "", nil, time.Time{}, fmt.Errorf("apidgrpc: decode realtime presence response: %w", err)
	}
	var observedAt time.Time
	if decoded.ObservedAt != nil {
		observedAt = *decoded.ObservedAt
	}
	return decoded.OwnMemberID, decoded.Members, observedAt, nil
}

func (c *RealtimeHistoryClientImpl) RelayEphemeral(ctx context.Context, endpointID, channel string, frame realtime.EphemeralFrame) error {
	if c == nil || c.conn == nil || c.nodeName == "" {
		return fmt.Errorf("apidgrpc: realtime ephemeral relay requires a connection and node name")
	}
	data, err := json.Marshal(realtimeFleetRelayRequest{NodeName: c.nodeName, EndpointID: endpointID, Channel: channel, Frame: frame})
	if err != nil {
		return fmt.Errorf("apidgrpc: encode realtime ephemeral event: %w", err)
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, realtimeFleetRelayMethod, wrapperspb.Bytes(data), response); err != nil {
		return fmt.Errorf("apidgrpc: relay realtime ephemeral event: %w", err)
	}
	return nil
}

func (c *RealtimeHistoryClientImpl) RegisterDirectMessageTargets(ctx context.Context, endpointID, messageID string, targets []state.ManagedRealtimeDirectMessageTarget) ([]string, error) {
	request := realtimeFleetDirectMessageRequest{Action: "register", NodeName: c.nodeName, EndpointID: endpointID, MessageID: messageID}
	request.Targets = make([]realtimeFleetDirectMessageTargetRequest, len(targets))
	for i, target := range targets {
		request.Targets[i] = realtimeFleetDirectMessageTargetRequest{ConnectionID: target.ConnectionID, AckSupported: target.AckSupported}
	}
	data, err := c.directMessageOperation(ctx, request)
	if err != nil {
		return nil, err
	}
	var response struct {
		CreatedConnectionIDs []string `json:"created_connection_ids"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("apidgrpc: decode direct message target result: %w", err)
	}
	return response.CreatedConnectionIDs, nil
}

func (c *RealtimeHistoryClientImpl) UpdateDirectMessageDeliveries(ctx context.Context, endpointID, messageID string, results []state.ManagedRealtimeDirectMessageDeliveryResult) error {
	request := realtimeFleetDirectMessageRequest{Action: "update", NodeName: c.nodeName, EndpointID: endpointID, MessageID: messageID}
	request.Results = make([]realtimeFleetDirectMessageResultRequest, len(results))
	for i, result := range results {
		request.Results[i] = realtimeFleetDirectMessageResultRequest{ConnectionID: result.ConnectionID, QueueStatus: result.QueueStatus}
	}
	_, err := c.directMessageOperation(ctx, request)
	return err
}

func (c *RealtimeHistoryClientImpl) AcknowledgeDirectMessage(ctx context.Context, endpointID, messageID, connectionID string) error {
	_, err := c.directMessageOperation(ctx, realtimeFleetDirectMessageRequest{
		Action: "acknowledge", NodeName: c.nodeName, EndpointID: endpointID, MessageID: messageID, ConnectionID: connectionID,
	})
	return err
}

func (c *RealtimeHistoryClientImpl) directMessageOperation(ctx context.Context, request realtimeFleetDirectMessageRequest) ([]byte, error) {
	if c == nil || c.conn == nil || c.nodeName == "" {
		return nil, fmt.Errorf("apidgrpc: realtime direct receipt requires a connection and node name")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: encode direct message receipt operation: %w", err)
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, realtimeFleetDirectMethod, wrapperspb.Bytes(data), response); err != nil {
		return nil, fmt.Errorf("apidgrpc: direct message receipt operation: %w", err)
	}
	return response.GetValue(), nil
}

var _ realtime.ManagedRealtimeFleetClient = (*RealtimeHistoryClientImpl)(nil)
var _ realtime.ManagedRealtimeDirectMessageReceiptClient = (*RealtimeHistoryClientImpl)(nil)
