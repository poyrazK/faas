package scheddgrpc

import (
	"context"
	"errors"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type databaseMigrationScheduler interface {
	LiveMigration(context.Context, string, dbsched.ImportSpec, bool) (*vmmdpb.DatabaseMigrationProgress, error)
	MigrationStatus(context.Context, string) (*vmmdpb.DatabaseMigrationProgress, error)
}

func (s *Server) migrationScheduler(ctx context.Context) (databaseMigrationScheduler, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	client, ok := d.(databaseMigrationScheduler)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database migration is unavailable")
	}
	return client, nil
}

func (s *Server) StartDatabaseMigration(ctx context.Context, req *scheddpb.ImportDatabaseRequest) (*vmmdpb.DatabaseMigrationProgress, error) {
	return s.controlDatabaseMigration(ctx, req, false)
}

func (s *Server) StopDatabaseMigration(ctx context.Context, req *scheddpb.ImportDatabaseRequest) (*vmmdpb.DatabaseMigrationProgress, error) {
	return s.controlDatabaseMigration(ctx, req, true)
}

func (s *Server) controlDatabaseMigration(ctx context.Context, req *scheddpb.ImportDatabaseRequest, stop bool) (*vmmdpb.DatabaseMigrationProgress, error) {
	client, err := s.migrationScheduler(ctx)
	if err != nil {
		return nil, err
	}
	p, err := client.LiveMigration(ctx, req.GetDatabaseId(), importSpec(req), stop)
	return p, s.migrationStatusError(err)
}

func (s *Server) DatabaseMigrationStatus(ctx context.Context, req *scheddpb.DatabaseRef) (*vmmdpb.DatabaseMigrationProgress, error) {
	client, err := s.migrationScheduler(ctx)
	if err != nil {
		return nil, err
	}
	p, err := client.MigrationStatus(ctx, req.GetDatabaseId())
	return p, s.migrationStatusError(err)
}

func (s *Server) migrationStatusError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dbsched.ErrImportUnsupported) {
		return status.Error(codes.Unimplemented, "database migration is unavailable")
	}
	return s.databaseStatus("migration", err)
}

func (s *Server) PrepareDatabaseMigration(ctx context.Context, req *scheddpb.ImportDatabaseRequest) (*vmmdpb.DatabaseMigrationProgress, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	client, ok := d.(interface {
		PrepareMigration(context.Context, string, dbsched.ImportSpec) (*vmmdpb.DatabaseMigrationProgress, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database migration preparation is unavailable")
	}
	p, err := client.PrepareMigration(ctx, req.GetDatabaseId(), importSpec(req))
	return p, s.migrationStatusError(err)
}

func (s *Server) VerifyDatabaseMigration(ctx context.Context, req *scheddpb.VerifyDatabaseMigrationRequest) (*vmmdpb.DatabaseMigrationProgress, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	if req.GetSource() == nil || !req.GetSourceWritesPaused() || !dbguest.ValidOperationID(req.GetVerificationId()) {
		return nil, status.Error(codes.InvalidArgument, "verification requires a source, a verification UUID and acknowledgement of paused source writes")
	}
	client, ok := d.(interface {
		VerifyMigration(context.Context, string, dbsched.ImportSpec, string) (*vmmdpb.DatabaseMigrationProgress, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "database migration verification is unavailable")
	}
	p, err := client.VerifyMigration(ctx, req.GetSource().GetDatabaseId(), importSpec(req.GetSource()), req.GetVerificationId())
	return p, s.migrationStatusError(err)
}

func (s *Server) ControlDatabaseMigrationActivation(ctx context.Context, req *scheddpb.DatabaseMigrationActivationRequest) (*scheddpb.DatabaseRef, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	op := dbguest.Op(req.GetOp())
	in := req.GetActivation()
	if !op.IsMigrationActivation() || in == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid migration activation operation")
	}
	a := dbmigration.Activation{SwitchID: in.GetSwitchId(), OperationID: in.GetOperationId(),
		SourcePin: in.GetSourcePin(), SourceDatabase: in.GetSourceDatabase(), Roles: in.GetRoles()}
	if err := (dbguest.Request{Op: op, Activation: &a}).Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid migration activation")
	}
	client, ok := d.(interface {
		ControlMigrationActivation(context.Context, string, dbguest.Op, dbmigration.Activation) error
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "migration activation is unavailable")
	}
	if err := s.migrationStatusError(client.ControlMigrationActivation(ctx, req.GetDatabaseId(), op, a)); err != nil {
		return nil, err
	}
	return &scheddpb.DatabaseRef{DatabaseId: req.GetDatabaseId()}, nil
}
