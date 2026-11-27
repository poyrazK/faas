package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type databaseRestoreDrills interface {
	RequestRestoreDrill(context.Context, string, string) (dbsched.Drill, error)
	GetRestoreDrill(context.Context, string, string) (dbsched.Drill, error)
}

func (s *Server) drillScheduler(ctx context.Context) (databaseRestoreDrills, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(databaseRestoreDrills)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database restore drills are unavailable")
	}
	return scheduler, nil
}

func (s *Server) RequestDatabaseRestoreDrill(ctx context.Context, req *scheddpb.RequestDatabaseRestoreDrillRequest) (*scheddpb.DatabaseRestoreDrill, error) {
	d, err := s.drillScheduler(ctx)
	if err != nil {
		return nil, err
	}
	drill, err := d.RequestRestoreDrill(ctx, req.GetDatabaseId(), req.GetRequestId())
	return s.databaseRestoreDrill(drill, err)
}

func (s *Server) GetDatabaseRestoreDrill(ctx context.Context, req *scheddpb.GetDatabaseRestoreDrillRequest) (*scheddpb.DatabaseRestoreDrill, error) {
	d, err := s.drillScheduler(ctx)
	if err != nil {
		return nil, err
	}
	drill, err := d.GetRestoreDrill(ctx, req.GetDatabaseId(), req.GetDrillId())
	return s.databaseRestoreDrill(drill, err)
}

func (s *Server) databaseRestoreDrill(d dbsched.Drill, err error) (*scheddpb.DatabaseRestoreDrill, error) {
	if err != nil {
		return nil, s.databaseStatus("restore drill", err)
	}
	out := &scheddpb.DatabaseRestoreDrill{
		DrillId: d.ID, RequestId: d.RequestID, Outcome: string(d.Result),
		StartedAt: timestamppb.New(d.StartedAt), ErrorCode: d.ErrorCode,
	}
	if !d.TargetAt.IsZero() {
		out.TargetAt = timestamppb.New(d.TargetAt)
	}
	if !d.FinishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(d.FinishedAt)
	}
	if !d.RecoveredTo.IsZero() {
		out.RecoveredTo = timestamppb.New(d.RecoveredTo)
	}
	return out, nil
}
