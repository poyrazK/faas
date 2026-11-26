package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbguest/diagnosticspb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) GetDatabaseDiagnostics(ctx context.Context, req *scheddpb.DatabaseDiagnosticsRequest) (*scheddpb.DatabaseDiagnosticsResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	control := dbguest.Request{Op: dbguest.OpDiagnostics, DiagnosticDatabase: req.GetDatabaseName(),
		TableHealthOptions: diagnosticspb.DecodeTableHealthOptions(req.GetTableHealthOptions()),
	}
	if err := control.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid diagnostic request")
	}
	if options := control.TableHealthOptions; options != nil {
		scheduler, ok := d.(interface {
			TableHealth(context.Context, string, string, dbguest.TableHealthOptions) (dbguest.TableHealth, error)
		})
		if !ok {
			return nil, status.Error(codes.Unimplemented, "table health is unavailable")
		}
		report, err := scheduler.TableHealth(ctx, req.GetDatabaseId(), req.GetDatabaseName(), *options)
		if err != nil {
			return nil, s.databaseStatus("table health", err)
		}
		return &scheddpb.DatabaseDiagnosticsResponse{State: report.State, TableHealth: diagnosticspb.EncodeTableHealth(report.Sample)}, nil
	}
	scheduler, ok := d.(interface {
		Diagnose(context.Context, string, string) (dbguest.Diagnostics, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database diagnostics are unavailable")
	}
	report, err := scheduler.Diagnose(ctx, req.GetDatabaseId(), req.GetDatabaseName())
	if err != nil {
		return nil, s.databaseStatus("diagnostics", err)
	}
	return &scheddpb.DatabaseDiagnosticsResponse{State: report.State, Activity: diagnosticspb.Encode(report.Sample)}, nil
}
