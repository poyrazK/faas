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

type slowRequestCapturer interface {
	QueueSlowRequestCapture(ctx context.Context, instanceID, requestID, traceID, route string, inflight time.Duration) (bool, string, error)
}

// ReportSlowRequest queues an automatic stacks+CPU capture of the instance
// serving a request the gateway saw in flight for too long (ADR-1155).
// Reports are advisory: an instance owned by another scheduler, or a skipped
// capture, is acknowledged with queued=false and a reason, not an error.
func (s *Server) ReportSlowRequest(ctx context.Context, req *scheddpb.ReportSlowRequestRequest) (resp *scheddpb.ReportSlowRequestAck, err error) {
	const op = "ReportSlowRequest"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), err) }()
	if req.GetInstanceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	if _, authErr := s.authorizeInstance(ctx, req.GetInstanceId()); authErr != nil {
		return &scheddpb.ReportSlowRequestAck{Reason: "instance is owned by another scheduler"}, nil
	}
	capturer, ok := s.engine.(slowRequestCapturer)
	if !ok {
		return &scheddpb.ReportSlowRequestAck{Reason: "this schedd does not queue automatic captures"}, nil
	}
	queued, reason, qerr := capturer.QueueSlowRequestCapture(ctx, req.GetInstanceId(), req.GetRequestId(), req.GetTraceId(), req.GetRoute(),
		time.Duration(req.GetInflightMs())*time.Millisecond)
	if errors.Is(qerr, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "instance not found")
	}
	if qerr != nil {
		return nil, status.Error(codes.Internal, "automatic profile capture could not be queued")
	}
	return &scheddpb.ReportSlowRequestAck{Queued: queued, Reason: reason}, nil
}

// ReportSlowRequest is the gateway's best-effort slow-request report. It is
// not part of ScheddClient: the gateway type-asserts it, so test doubles and
// older clients need not implement it.
func (c *Client) ReportSlowRequest(ctx context.Context, instanceID, requestID, traceID, route string, inflight time.Duration) (bool, string, error) {
	resp, err := c.cli.ReportSlowRequest(ctx, &scheddpb.ReportSlowRequestRequest{InstanceId: instanceID, RequestId: requestID,
		TraceId: traceID, Route: route, InflightMs: inflight.Milliseconds()})
	if err != nil {
		return false, "", liftErr(err)
	}
	return resp.GetQueued(), resp.GetReason(), nil
}
