package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbguest/diagnosticspb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) DatabaseAutovacuum(ctx context.Context, req *scheddpb.DatabaseAutovacuumRequest) (*scheddpb.DatabaseAutovacuumResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(interface {
		Autovacuum(context.Context, string, dbguest.AutovacuumProfile) (dbguest.AutovacuumStatus, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "autovacuum profiles are unavailable")
	}
	report, err := scheduler.Autovacuum(ctx, req.GetDatabaseId(), dbguest.AutovacuumProfile(req.GetProfile()))
	if err != nil {
		return nil, s.databaseStatus("autovacuum", err)
	}
	return &scheddpb.DatabaseAutovacuumResponse{
		Profile: string(report.Profile), State: report.State,
		Effective: diagnosticspb.EncodeAutovacuum(report.Effective), ReplicaEffective: diagnosticspb.EncodeAutovacuum(report.ReplicaEffective),
	}, nil
}
