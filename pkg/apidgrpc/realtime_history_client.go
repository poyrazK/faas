package apidgrpc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// RealtimeHistoryClient is the private control-plane reader for realtimed.
// Callers must authorize channel membership before exposing a page to a user.
type RealtimeHistoryClient interface {
	ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error)
	ReportChannelRoute(context.Context, string, string, bool) error
	Close() error
}

type RealtimeHistoryClientImpl struct {
	conn     *grpc.ClientConn
	cli      apidpb.RealtimeHistoryClient
	nodeName string
}

var _ RealtimeHistoryClient = (*RealtimeHistoryClientImpl)(nil)

func DialRealtimeHistory(ctx context.Context, target string, tlsCfg *tls.Config, nodeName string) (*RealtimeHistoryClientImpl, error) {
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial realtime history: %w", err)
	}
	return NewRealtimeHistoryClientForNode(conn, nodeName), nil
}

func NewRealtimeHistoryClient(conn *grpc.ClientConn) *RealtimeHistoryClientImpl {
	return NewRealtimeHistoryClientForNode(conn, "")
}

func NewRealtimeHistoryClientForNode(conn *grpc.ClientConn, nodeName string) *RealtimeHistoryClientImpl {
	return &RealtimeHistoryClientImpl{conn: conn, cli: apidpb.NewRealtimeHistoryClient(conn), nodeName: nodeName}
}

func (c *RealtimeHistoryClientImpl) ReadChannelHistory(ctx context.Context, endpointID, channel string, after int64, limit int) (state.ManagedRealtimeChannelHistory, error) {
	if c.nodeName != "" {
		response, err := c.inboxOperation(ctx, realtimeInboxRequest{Action: "channel_read", EndpointID: endpointID, Channel: channel, Sequence: after, Limit: limit})
		return response.History, err
	}
	if endpointID == "" || channel == "" || after < 0 || limit < 1 || limit > state.ManagedRealtimeHistoryMaxRead {
		return state.ManagedRealtimeChannelHistory{}, state.ErrManagedRealtimeHistoryInvalid
	}
	response, err := c.cli.ReadChannelHistory(ctx, &apidpb.ReadChannelHistoryRequest{
		EndpointId: endpointID, Channel: channel, AfterSequence: after, Limit: int32(limit),
	})
	if err != nil {
		return state.ManagedRealtimeChannelHistory{}, fmt.Errorf("apidgrpc: read realtime history: %w", err)
	}
	history := state.ManagedRealtimeChannelHistory{
		OldestSequence:     response.GetOldestSequence(),
		LatestSequence:     response.GetLatestSequence(),
		HistoryUnavailable: response.GetHistoryUnavailable(),
	}
	if history.HistoryUnavailable {
		return history, nil
	}
	for _, message := range response.GetMessages() {
		var metadata map[string]string
		if len(message.GetMetadataJson()) > 0 {
			if err := json.Unmarshal(message.GetMetadataJson(), &metadata); err != nil {
				return state.ManagedRealtimeChannelHistory{}, err
			}
		}
		history.Messages = append(history.Messages, state.ManagedRealtimeChannelMessage{
			EndpointID: endpointID, Channel: channel,
			Metadata: metadata, Sequence: message.GetSequence(), Data: append([]byte(nil), message.GetData()...),
			Binary: message.GetBinary(), CreatedAt: time.Unix(0, message.GetCreatedAtUnixNano()).UTC(),
		})
	}
	return history, nil
}

func (c *RealtimeHistoryClientImpl) LoadDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, initialSequence int64) (int64, error) {
	if c == nil || c.cli == nil {
		return 0, fmt.Errorf("apidgrpc: realtime durable cursor client is unavailable")
	}
	response, err := c.cli.GetDurableCursor(ctx, &apidpb.DurableCursorRequest{
		EndpointId: endpointID, Principal: principal, Subscription: subscription, Channel: channel, InitialSequence: initialSequence,
	})
	if err != nil {
		return 0, fmt.Errorf("apidgrpc: load realtime durable cursor: %w", err)
	}
	return response.GetSequence(), nil
}

func (c *RealtimeHistoryClientImpl) AdvanceDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	return c.mutateDurableCursor(ctx, apidpb.RealtimeHistory_AdvanceDurableCursor_FullMethodName, endpointID, principal, subscription, channel, sequence)
}

func (c *RealtimeHistoryClientImpl) ResetDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	return c.mutateDurableCursor(ctx, apidpb.RealtimeHistory_ResetDurableCursor_FullMethodName, endpointID, principal, subscription, channel, sequence)
}

func (c *RealtimeHistoryClientImpl) mutateDurableCursor(ctx context.Context, method, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	if c == nil || c.cli == nil {
		return 0, fmt.Errorf("apidgrpc: realtime durable cursor client is unavailable")
	}
	req := &apidpb.AdvanceDurableCursorRequest{
		EndpointId: endpointID, Principal: principal, Subscription: subscription, Channel: channel, Sequence: sequence,
	}
	var response *apidpb.DurableCursorResponse
	var err error
	switch method {
	case apidpb.RealtimeHistory_AdvanceDurableCursor_FullMethodName:
		response, err = c.cli.AdvanceDurableCursor(ctx, req)
	case apidpb.RealtimeHistory_ResetDurableCursor_FullMethodName:
		response, err = c.cli.ResetDurableCursor(ctx, req)
	default:
		return 0, fmt.Errorf("apidgrpc: unsupported durable cursor operation")
	}
	if err != nil {
		return 0, fmt.Errorf("apidgrpc: mutate realtime durable cursor: %w", err)
	}
	return response.GetSequence(), nil
}

func (c *RealtimeHistoryClientImpl) ReportChannelRoute(ctx context.Context, endpointID, channel string, subscribed bool) error {
	if c == nil || c.cli == nil || c.nodeName == "" || endpointID == "" || channel == "" {
		return fmt.Errorf("apidgrpc: realtime channel route report requires a connection, node name, endpoint, and channel")
	}
	_, err := c.cli.ReportChannelRoute(ctx, &apidpb.ReportChannelRouteRequest{
		EndpointId: endpointID, Channel: channel, NodeName: c.nodeName, Subscribed: subscribed,
	})
	if err != nil {
		return fmt.Errorf("apidgrpc: report realtime channel route: %w", err)
	}
	return nil
}

func (c *RealtimeHistoryClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
