package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type databaseUpgradeRehearsals interface {
	RequestUpgradeRehearsal(context.Context, string, string, int, string) (dbsched.Drill, error)
	GetUpgradeRehearsal(context.Context, string, string) (dbsched.Drill, error)
}

func (s *Server) rehearsalScheduler(ctx context.Context) (databaseUpgradeRehearsals, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	scheduler, ok := d.(databaseUpgradeRehearsals)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database upgrade rehearsals are unavailable")
	}
	return scheduler, nil
}

func (s *Server) RequestDatabaseUpgradeRehearsal(ctx context.Context, req *scheddpb.RequestDatabaseUpgradeRehearsalRequest) (*scheddpb.DatabaseUpgradeRehearsal, error) {
	d, err := s.rehearsalScheduler(ctx)
	if err != nil {
		return nil, err
	}
	rehearsal, err := d.RequestUpgradeRehearsal(ctx, req.GetDatabaseId(), req.GetRequestId(), int(req.GetPostgresMajor()), req.GetDatabaseName())
	return s.databaseUpgradeRehearsal(rehearsal, err)
}

func (s *Server) GetDatabaseUpgradeRehearsal(ctx context.Context, req *scheddpb.GetDatabaseUpgradeRehearsalRequest) (*scheddpb.DatabaseUpgradeRehearsal, error) {
	d, err := s.rehearsalScheduler(ctx)
	if err != nil {
		return nil, err
	}
	rehearsal, err := d.GetUpgradeRehearsal(ctx, req.GetDatabaseId(), req.GetRehearsalId())
	return s.databaseUpgradeRehearsal(rehearsal, err)
}

func (s *Server) databaseUpgradeRehearsal(d dbsched.Drill, err error) (*scheddpb.DatabaseUpgradeRehearsal, error) {
	if err != nil {
		return nil, s.databaseStatus("upgrade rehearsal", err)
	}
	drill, err := s.databaseRestoreDrill(d, nil)
	if err != nil {
		return nil, err
	}
	r := d.Rehearsal
	out := &scheddpb.DatabaseUpgradeRehearsal{Drill: drill, SourceMajor: int32(r.SourceMajor), TargetMajor: int32(r.TargetMajor)}
	switch {
	case d.PendingOutcome != "":
		drill.Outcome = "cleaning_up"
		out.Phase = "cleanup"
		if d.PendingOutcome != dbsched.DrillOK {
			out.FailedPhase = r.Phase
		}
	case d.Result == dbsched.DrillFailed:
		out.FailedPhase = r.Phase
	case d.Result == dbsched.DrillQueued && r.RestoreStarted:
		drill.Outcome = string(dbsched.DrillRunning)
		out.Phase = r.Phase
	}
	return out, nil
}
