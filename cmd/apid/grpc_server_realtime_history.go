package main

import (
	"context"
	"errors"
	"strings"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// History reads and channel route reports are available only on apid's private
// daemon listeners. Customer endpoint and channel authorization remains in
// realtimed; these RPCs are not customer authorization boundaries.
type realtimeHistoryReaderStore interface {
	state.ManagedRealtimeHistoryStore
	ManagedRealtimeEndpointByID(context.Context, string) (state.ManagedRealtimeEndpoint, error)
}

type realtimeHistoryNodeStore interface {
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
}

type realtimeHistoryReceiver struct {
	apidpb.UnimplementedRealtimeHistoryServer
	store   realtimeHistoryReaderStore
	nodes   realtimeHistoryNodeStore
	routes  state.ManagedRealtimeChannelRouteStore
	remover state.ManagedRealtimeChannelRouteRemover
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

func (r *realtimeHistoryReceiver) ReportChannelRoute(ctx context.Context, req *apidpb.ReportChannelRouteRequest) (*apidpb.ReportChannelRouteResponse, error) {
	if req == nil || req.GetEndpointId() == "" || !realtime.ValidateChannel(req.GetChannel()) ||
		req.GetNodeName() == "" || req.GetNodeName() != strings.TrimSpace(req.GetNodeName()) {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime channel route")
	}
	if r.store == nil || r.nodes == nil {
		return nil, status.Error(codes.Unavailable, "realtime channel route store unavailable")
	}
	if r.routes == nil || !req.GetSubscribed() && r.remover == nil {
		return nil, status.Error(codes.Unavailable, "realtime channel route updates unavailable")
	}
	endpoint, err := r.store.ManagedRealtimeEndpointByID(ctx, req.GetEndpointId())
	if errors.Is(err, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "realtime endpoint not found")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "realtime endpoint lookup unavailable")
	}
	if req.GetSubscribed() && !endpoint.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "realtime endpoint disabled")
	}
	node, err := r.nodes.ComputeNodeByName(ctx, req.GetNodeName())
	if errors.Is(err, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "realtime node not found")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "realtime node lookup unavailable")
	}
	if req.GetSubscribed() && !node.Active {
		return nil, status.Error(codes.FailedPrecondition, "realtime node inactive")
	}
	lock, err := r.routes.AcquireManagedRealtimeChannelRouteLock(ctx, node.ID)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "realtime channel route lock unavailable")
	}
	if lock == nil {
		return nil, status.Error(codes.Unavailable, "realtime channel route lock unavailable")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		lock.Release(releaseCtx)
		cancel()
	}()
	route := state.ManagedRealtimeChannelRoute{EndpointID: req.GetEndpointId(), Channel: req.GetChannel(), NodeID: node.ID}
	if req.GetSubscribed() {
		err = r.routes.AddManagedRealtimeChannelRoutes(ctx, []state.ManagedRealtimeChannelRoute{route})
	} else {
		err = r.remover.RemoveManagedRealtimeChannelRoute(ctx, route)
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "realtime channel route update unavailable")
	}
	return &apidpb.ReportChannelRouteResponse{Applied: true}, nil
}

func registerRealtimeHistoryReceiver(server *grpc.Server, store state.Store) {
	reader, _ := store.(realtimeHistoryReaderStore)
	nodes, _ := store.(realtimeHistoryNodeStore)
	routes, _ := store.(state.ManagedRealtimeChannelRouteStore)
	remover, _ := store.(state.ManagedRealtimeChannelRouteRemover)
	apidpb.RegisterRealtimeHistoryServer(server, &realtimeHistoryReceiver{store: reader, nodes: nodes, routes: routes, remover: remover})
}
