package receivers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// realtimeConnectionSyncMaxBytes bounds a sync request: up to
// api.RealtimeConnectionSyncMaxAccounts accounts of ~70 JSON bytes each.
const realtimeConnectionSyncMaxBytes = 1 << 20

type realtimeConnectionSyncMessage struct {
	NodeName string                             `json:"node_name,omitempty"`
	Accounts []state.RealtimeAccountConnections `json:"accounts"`
}

// SyncConnections stores a registered node's per-account connection counts
// and answers with the other nodes' fresh counts (ADR-1323 fair use).
func (r *realtimeFleetReceiver) SyncConnections(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.connections == nil {
		return nil, status.Error(codes.Unavailable, "realtime connection counts unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > realtimeConnectionSyncMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime connection sync")
	}
	var req realtimeConnectionSyncMessage
	if err := decodeRealtimeFleetJSON(envelope.GetValue(), &req); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime connection sync")
	}
	if _, err := r.lookupNode(ctx, req.NodeName); err != nil {
		return nil, err
	}
	remote, err := r.connections.SyncRealtimeNodeConnections(ctx, req.NodeName, req.Accounts)
	switch {
	case errors.Is(err, state.ErrRealtimeConnectionSyncInvalid):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		return nil, status.Error(codes.Unavailable, "realtime connection counts unavailable")
	}
	data, err := json.Marshal(realtimeConnectionSyncMessage{Accounts: remote})
	if err != nil {
		return nil, status.Error(codes.Internal, "encode realtime connection counts")
	}
	return wrapperspb.Bytes(data), nil
}
