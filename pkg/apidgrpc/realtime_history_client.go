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
	Close() error
}

type RealtimeHistoryClientImpl struct {
	conn *grpc.ClientConn
	cli  apidpb.RealtimeHistoryClient
}

var _ RealtimeHistoryClient = (*RealtimeHistoryClientImpl)(nil)

func DialRealtimeHistory(ctx context.Context, target string, tlsCfg *tls.Config) (*RealtimeHistoryClientImpl, error) {
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial realtime history: %w", err)
	}
	return NewRealtimeHistoryClient(conn), nil
}

func NewRealtimeHistoryClient(conn *grpc.ClientConn) *RealtimeHistoryClientImpl {
	return &RealtimeHistoryClientImpl{conn: conn, cli: apidpb.NewRealtimeHistoryClient(conn)}
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

func (c *RealtimeHistoryClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
