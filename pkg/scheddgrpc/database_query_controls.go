package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbguest/diagnosticspb"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListDatabaseSessions(ctx context.Context, req *scheddpb.ListDatabaseSessionsRequest) (*scheddpb.ListDatabaseSessionsResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	control := dbguest.Request{Op: dbguest.OpSessions, DiagnosticDatabase: req.GetDatabaseName()}
	if control.Validate() != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid session inspection request")
	}
	scheduler, ok := d.(interface {
		ListSessions(context.Context, string, string) (dbguest.Sessions, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "session inspection is unavailable")
	}
	sample, err := scheduler.ListSessions(ctx, req.GetDatabaseId(), req.GetDatabaseName())
	if err != nil {
		return nil, s.databaseStatus("list sessions", err)
	}
	return &scheddpb.ListDatabaseSessionsResponse{State: sample.State, Sample: diagnosticspb.EncodeSessions(sample.Sample)}, nil
}

func (s *Server) CancelDatabaseQuery(ctx context.Context, req *scheddpb.CancelDatabaseQueryRequest) (*scheddpb.CancelDatabaseQueryResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(interface {
		CancelQuery(context.Context, string, string) (string, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "query cancellation is unavailable")
	}
	outcome, err := scheduler.CancelQuery(ctx, req.GetDatabaseId(), req.GetTargetId())
	if err != nil {
		return nil, s.databaseStatus("cancel query", err)
	}
	return &scheddpb.CancelDatabaseQueryResponse{Outcome: outcome}, nil
}

func (s *Server) ExchangeDatabaseQueryControls(ctx context.Context, req *scheddpb.ExchangeDatabaseQueryControlsRequest) (*scheddpb.ExchangeDatabaseQueryControlsResponse, error) {
	d, err := s.databaseScheduler(ctx, callerPgproxy)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(interface {
		ExchangeQueryControls(context.Context, string, []dbquerycontrol.Target, []dbsched.QueryControlResult) ([]dbquerycontrol.Command, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "query controls are unavailable")
	}
	if len(req.GetActive()) > dbquerycontrol.MaxTargets || len(req.GetResults()) > dbquerycontrol.MaxCommands {
		return nil, status.Error(codes.InvalidArgument, "oversized query controls exchange")
	}
	active := make([]dbquerycontrol.Target, 0, len(req.GetActive()))
	for _, t := range req.GetActive() {
		active = append(active, dbquerycontrol.FromProto(t))
	}
	results := make([]dbsched.QueryControlResult, 0, len(req.GetResults()))
	for _, r := range req.GetResults() {
		results = append(results, dbsched.QueryControlResult{TargetID: r.GetTargetId(), Outcome: r.GetOutcome()})
	}
	pending, err := scheduler.ExchangeQueryControls(ctx, req.GetOwnerId(), active, results)
	if err != nil {
		return nil, s.databaseStatus("exchange query controls", err)
	}
	out := &scheddpb.ExchangeDatabaseQueryControlsResponse{}
	for _, t := range pending {
		out.Cancel = append(out.Cancel, &scheddpb.DatabaseQueryCommand{Target: t.Target.Proto(), ExpiresAt: timestamppb.New(t.ExpiresAt)})
	}
	return out, nil
}
