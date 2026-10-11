package apidgrpc

import (
	"context"
	"crypto/tls"
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
		history.Messages = append(history.Messages, state.ManagedRealtimeChannelMessage{
			EndpointID: endpointID, Channel: channel,
			Sequence: message.GetSequence(), Data: append([]byte(nil), message.GetData()...),
			Binary: message.GetBinary(), CreatedAt: time.Unix(0, message.GetCreatedAtUnixNano()).UTC(),
		})
	}
	return history, nil
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
