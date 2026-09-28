package main

import (
	"context"
	"errors"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The history RPC is available only on apid's private daemon listeners. A
// client subscription must pass realtimed's endpoint and channel checks before
// it calls this reader; the RPC is not a customer authorization boundary.
type realtimeHistoryReaderStore interface {
	state.ManagedRealtimeHistoryStore
	ManagedRealtimeEndpointByID(context.Context, string) (state.ManagedRealtimeEndpoint, error)
}

type realtimeHistoryReceiver struct {
	apidpb.UnimplementedRealtimeHistoryServer
	store realtimeHistoryReaderStore
}

func (r *realtimeHistoryReceiver) ReadChannelHistory(ctx context.Context, req *apidpb.ReadChannelHistoryRequest) (*apidpb.ReadChannelHistoryResponse, error) {
	if r.store == nil {
		return nil, status.Error(codes.Unavailable, "realtime history store unavailable")
	}
	if req == nil || req.GetEndpointId() == "" || req.GetChannel() == "" || req.GetAfterSequence() < 0 ||
		req.GetLimit() < 1 || req.GetLimit() > state.ManagedRealtimeHistoryMaxRead {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime history request")
	}
	endpoint, err := r.store.ManagedRealtimeEndpointByID(ctx, req.GetEndpointId())
	if errors.Is(err, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "realtime endpoint not found")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "realtime endpoint lookup unavailable")
	}
	if !endpoint.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "realtime endpoint disabled")
	}
	history, err := r.store.ReadManagedRealtimeChannelHistory(ctx, req.GetEndpointId(), req.GetChannel(), req.GetAfterSequence(), int(req.GetLimit()))
	switch {
	case errors.Is(err, state.ErrManagedRealtimeHistoryInvalid):
		return nil, status.Error(codes.InvalidArgument, "invalid realtime history request")
	case errors.Is(err, state.ErrNotFound):
		return nil, status.Error(codes.NotFound, "realtime endpoint not found")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "realtime history unavailable")
	}
	response := &apidpb.ReadChannelHistoryResponse{
		OldestSequence:     history.OldestSequence,
		LatestSequence:     history.LatestSequence,
		HistoryUnavailable: history.HistoryUnavailable,
	}
	if history.HistoryUnavailable {
		return response, nil
	}
	for _, message := range history.Messages {
		response.Messages = append(response.Messages, &apidpb.RetainedChannelMessage{
			Sequence:          message.Sequence,
			Data:              append([]byte(nil), message.Data...),
			Binary:            message.Binary,
			CreatedAtUnixNano: message.CreatedAt.UnixNano(),
		})
	}
	return response, nil
}

func registerRealtimeHistoryReceiver(server *grpc.Server, store state.Store) {
	reader, _ := store.(realtimeHistoryReaderStore)
	apidpb.RegisterRealtimeHistoryServer(server, &realtimeHistoryReceiver{store: reader})
}
