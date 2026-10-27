package scheddgrpc

import (
	"context"
	"errors"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type frameworkReadyEngine interface {
	ReportFrameworkReady(context.Context, string) error
}

func (s *Server) ReportFrameworkReady(ctx context.Context, req *scheddpb.FrameworkReadyReport) (*scheddpb.FrameworkReadyAck, error) {
	id := req.GetInstanceId()
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	if _, err := s.authorizeInstance(ctx, id); err != nil {
		relayed, relayErr := s.relayForeignFrameworkReady(ctx, err, id)
		if !relayed {
			return nil, relayErr
		}
		return &scheddpb.FrameworkReadyAck{}, nil
	}
	engine, ok := s.engine.(frameworkReadyEngine)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "framework readiness unavailable")
	}
	start := time.Now()
	err := engine.ReportFrameworkReady(ctx, id)
	s.ops.Observe("ReportFrameworkReady", time.Since(start), err)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "instance not found")
		}
		if errors.Is(err, state.ErrConflict) {
			return nil, status.Error(codes.FailedPrecondition, "instance is not running")
		}
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &scheddpb.FrameworkReadyAck{}, nil
}

// ForeignFrameworkReadyRelay publishes a framework-ready receipt for the
// schedd that owns the app. *sched.Engine implements it.
type ForeignFrameworkReadyRelay interface {
	RelayFrameworkReady(ctx context.Context, instanceID, appID string) error
}

// relayForeignFrameworkReady hands vmmd's framework-ready receipt for an
// instance this node hosts, but whose app a peer schedd owns, to that owner
// (Hunt 14 H14-104). It uses the failure relay's guards: a FailedPrecondition
// ownership rejection, a configured relay, and an instance on this node.
func (s *Server) relayForeignFrameworkReady(ctx context.Context, authErr error, instanceID string) (bool, error) {
	relay, ok := s.failureRelay.(ForeignFrameworkReadyRelay)
	if !ok || s.resolver == nil || status.Code(authErr) != codes.FailedPrecondition {
		return false, authErr
	}
	ins, err := s.resolver.InstanceByID(ctx, instanceID)
	if err != nil || ins.NodeID == "" || ins.NodeID != string(s.owner) {
		return false, authErr
	}
	if err := relay.RelayFrameworkReady(ctx, instanceID, ins.AppID); err != nil {
		s.log.Warn("schedd: relay foreign framework ready", "instance", instanceID, "err", err)
		return false, status.Errorf(codes.Unavailable, "relay framework-ready report for instance %s: %v", instanceID, err)
	}
	return true, nil
}
