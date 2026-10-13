package scheddgrpc

import (
	"context"
	"errors"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type memoryPressureCapturer interface {
	QueueMemoryPressureCapture(ctx context.Context, instanceID string, availableMB, totalMB, anonMB int) (bool, string, error)
}

// ReportMemoryPressure queues an automatic heap capture for a guest that
// reported memory pressure (ADR-1155). Reports are advisory: an instance owned
// by another scheduler, or a skipped capture, is acknowledged with
// queued=false and a reason rather than an error.
func (s *Server) ReportMemoryPressure(ctx context.Context, req *scheddpb.ReportMemoryPressureRequest) (resp *scheddpb.ReportMemoryPressureAck, err error) {
	const op = "ReportMemoryPressure"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), err) }()
	if req.GetInstanceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	if _, authErr := s.authorizeInstance(ctx, req.GetInstanceId()); authErr != nil {
		return &scheddpb.ReportMemoryPressureAck{Reason: "instance is owned by another scheduler"}, nil
	}
	capturer, ok := s.engine.(memoryPressureCapturer)
	if !ok {
		return &scheddpb.ReportMemoryPressureAck{Reason: "this schedd does not queue automatic captures"}, nil
	}
	queued, reason, qerr := capturer.QueueMemoryPressureCapture(ctx, req.GetInstanceId(), int(req.GetAvailableMb()), int(req.GetTotalMb()), int(req.GetAnonMb()))
	if errors.Is(qerr, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "instance not found")
	}
	if qerr != nil {
		return nil, status.Error(codes.Internal, "automatic profile capture could not be queued")
	}
	return &scheddpb.ReportMemoryPressureAck{Queued: queued, Reason: reason}, nil
}
