// Package scheddgrpc turns pkg/sched.Engine into the gRPC service defined in
// api/proto/onebox/faas/schedd/v1 (ADR-018). Handlers stay thin — each wraps a
// single Engine call and translates its result into the proto envelope, routing
// errors through pkg/grpcerr so the gateway maps them straight to RFC 7807.
// Mirrors pkg/vmmdgrpc on the vmmd side.

package scheddgrpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SchedAPI is the slice of pkg/sched.Engine the handlers need. Defined here (not
// imported as a concrete type) so unit tests can pass a fake without standing up
// a store + vmmd.
type SchedAPI interface {
	Wake(ctx context.Context, appID string) (sched.WakeResult, error)
	ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error)
	// ParkWithReason is the meterd-triggered variant (M7, spec §4.7).
	// The reason string is for the audit log; the park semantics are
	// identical to the idle-reaper Park.
	ParkWithReason(ctx context.Context, instanceID, reason string) error
}

// Server implements scheddpb.ScheddServer.
type Server struct {
	scheddpb.UnimplementedScheddServer

	engine SchedAPI
	ops    *wire.OpsMetrics
	log    *slog.Logger
}

// New wires the server. ops may be nil (a throwaway registry); log may be nil
// (slog default).
func New(engine SchedAPI, ops *wire.OpsMetrics, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("schedd_test")
	}
	return &Server{engine: engine, ops: ops, log: log}
}

// Register binds s to a gRPC server.
func (s *Server) Register(g *grpc.Server) {
	scheddpb.RegisterScheddServer(g, s)
}

// Wake asks the engine to bring up an instance for the app and returns its
// address. Admission denials arrive as *api.Problem and travel as a
// ResourceExhausted status the gateway turns into a 503.
func (s *Server) Wake(ctx context.Context, req *scheddpb.WakeRequest) (*scheddpb.WakeResponse, error) {
	const op = "Wake"
	start := time.Now()
	res, err := s.engine.Wake(ctx, req.GetAppId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &scheddpb.WakeResponse{
		InstanceId: res.InstanceID,
		NodeId:     res.NodeID,
		Method:     mapMethod(res.Method),
		WakeId:     res.WakeID,
	}, nil
}

// ReportActivity persists a last_request_at batch from the gateway.
func (s *Server) ReportActivity(ctx context.Context, req *scheddpb.ReportActivityRequest) (*scheddpb.ReportActivityResponse, error) {
	const op = "ReportActivity"
	start := time.Now()
	in := req.GetTouches()
	touches := make([]state.InstanceTouch, 0, len(in))
	for _, t := range in {
		touches = append(touches, state.InstanceTouch{
			InstanceID:  t.GetInstanceId(),
			LastRequest: time.UnixMilli(t.GetUnixMs()),
		})
	}
	applied, err := s.engine.ReportActivity(ctx, touches)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &scheddpb.ReportActivityResponse{Applied: int32(applied)}, nil
}

// ParkInstance is the meterd-driven explicit park (M7, spec §4.7).
// Idempotent: parking an already-parked instance is a no-op + Ok=true.
func (s *Server) ParkInstance(ctx context.Context, req *scheddpb.ParkInstanceRequest) (*scheddpb.ParkInstanceResponse, error) {
	const op = "ParkInstance"
	start := time.Now()
	err := s.engine.ParkWithReason(ctx, req.GetInstanceId(), req.GetReason())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		// ErrNotFound → NotFound status; everything else Internal.
		if errors.Is(err, state.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &scheddpb.ParkInstanceResponse{Ok: true}, nil
}

// mapMethod translates the engine's vmmd-side WakeMethod to the schedd wire
// enum. The two enums share values (0=cold boot, 1=restore); the switch keeps
// them decoupled if either drifts.
func mapMethod(m vmmdpb.WakeMethod) scheddpb.WakeMethod {
	if m == vmmdpb.WakeMethod_WAKE_RESTORE {
		return scheddpb.WakeMethod_WAKE_RESTORE
	}
	return scheddpb.WakeMethod_WAKE_COLD_BOOT
}

// toProblem lifts a plain error to *api.Problem if it isn't one already, so
// internal go-stack details never leak across the wire.
func toProblem(err error) *api.Problem {
	if err == nil {
		return nil
	}
	if p := api.AsProblem(err); p != nil {
		return p
	}
	return api.NewProblem(int(codes.Internal), "internal", "schedd operation failed", err.Error())
}
