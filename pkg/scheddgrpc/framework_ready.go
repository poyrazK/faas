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
	if _, err := authorizeInstance(ctx, s.owner, s.resolver, id); err != nil {
		return nil, err
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
