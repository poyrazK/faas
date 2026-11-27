package firecracker

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbmigration"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) CheckMigration(ctx context.Context, request managedpostgres.ImportRequest) (api.ManagedPostgresMigrationCheck, error) {
	req, err := p.importRequest(request)
	if err != nil {
		return api.ManagedPostgresMigrationCheck{}, err
	}
	client, ok := p.scheduler.(interface {
		CheckDatabaseMigration(context.Context, *scheddpb.ImportDatabaseRequest, ...grpc.CallOption) (*vmmdpb.DatabaseMigrationCheck, error)
	})
	if !ok {
		return api.ManagedPostgresMigrationCheck{}, managedpostgres.ErrUnsupported
	}
	result, err := client.CheckDatabaseMigration(ctx, req)
	if status.Code(err) == codes.Unimplemented {
		return api.ManagedPostgresMigrationCheck{}, managedpostgres.ErrUnsupported
	}
	if err != nil {
		return api.ManagedPostgresMigrationCheck{}, providerError(err)
	}
	switch result.GetOutcome() {
	case dbmigration.Compatible, dbmigration.Blocked, dbmigration.Unverified:
	default:
		return api.ManagedPostgresMigrationCheck{}, managedpostgres.ErrUnavailable
	}
	return api.ManagedPostgresMigrationCheck{
		Outcome: result.GetOutcome(), Reasons: result.GetReasons(),
		SourceMajor: int(result.GetSourceMajor()), TargetMajor: int(result.GetTargetMajor()),
		TableCount: result.GetTableCount(), SequenceCount: result.GetSequenceCount(),
		UnsupportedTableCount: result.GetUnsupportedTableCount(), UnsupportedTables: dbmigration.ReportableTables(result.GetUnsupportedTables()),
		SourceBytes: result.GetSourceBytes(), TargetFreeBytes: result.GetTargetFreeBytes(),
		SourceWALLimitBytes: result.GetSourceWalLimitBytes(), FreeSourceSlots: result.GetFreeSourceSlots(),
		FreeSourceSenders: result.GetFreeSourceSenders(),
	}, nil
}

type databaseMigrationClient interface {
	StartDatabaseMigration(context.Context, *scheddpb.ImportDatabaseRequest, ...grpc.CallOption) (*vmmdpb.DatabaseMigrationProgress, error)
	StopDatabaseMigration(context.Context, *scheddpb.ImportDatabaseRequest, ...grpc.CallOption) (*vmmdpb.DatabaseMigrationProgress, error)
	DatabaseMigrationStatus(context.Context, *scheddpb.DatabaseRef, ...grpc.CallOption) (*vmmdpb.DatabaseMigrationProgress, error)
}

func (p *Provider) LiveMigration(ctx context.Context, request managedpostgres.ImportRequest, stop bool) (api.ManagedPostgresMigrationProgress, error) {
	client, ok := p.scheduler.(databaseMigrationClient)
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, managedpostgres.ErrUnsupported
	}
	req, err := p.importRequest(request)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	operation := client.StartDatabaseMigration
	if stop {
		operation = client.StopDatabaseMigration
	}
	result, err := operation(ctx, req)
	progress, err := migrationProgress(result, err)
	if err == nil && progress.OperationID != request.OperationID {
		err = managedpostgres.ErrUnavailable
	}
	return progress, err
}

func (p *Provider) PrepareMigration(ctx context.Context, request managedpostgres.ImportRequest) (api.ManagedPostgresMigrationProgress, error) {
	client, ok := p.scheduler.(interface {
		PrepareDatabaseMigration(context.Context, *scheddpb.ImportDatabaseRequest, ...grpc.CallOption) (*vmmdpb.DatabaseMigrationProgress, error)
	})
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, managedpostgres.ErrUnsupported
	}
	req, err := p.importRequest(request)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	result, err := client.PrepareDatabaseMigration(ctx, req)
	progress, err := migrationProgress(result, err)
	if err == nil && (progress.OperationID != request.OperationID || progress.Preparation == nil) {
		err = managedpostgres.ErrUnavailable
	}
	return progress, err
}

func (p *Provider) VerifyMigration(ctx context.Context, request managedpostgres.ImportRequest, verification string) (api.ManagedPostgresMigrationProgress, error) {
	client, ok := p.scheduler.(interface {
		VerifyDatabaseMigration(context.Context, *scheddpb.VerifyDatabaseMigrationRequest, ...grpc.CallOption) (*vmmdpb.DatabaseMigrationProgress, error)
	})
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, managedpostgres.ErrUnsupported
	}
	req, err := p.importRequest(request)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	result, err := client.VerifyDatabaseMigration(ctx, &scheddpb.VerifyDatabaseMigrationRequest{
		Source: req, VerificationId: verification, SourceWritesPaused: true,
	})
	progress, err := migrationProgress(result, err)
	if err == nil && (progress.OperationID != request.OperationID || progress.Verification == nil || progress.Verification.ID != verification) {
		err = managedpostgres.ErrUnavailable
	}
	return progress, err
}

func (p *Provider) MigrationStatus(ctx context.Context, resourceID string) (api.ManagedPostgresMigrationProgress, error) {
	client, ok := p.scheduler.(databaseMigrationClient)
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, managedpostgres.ErrUnsupported
	}
	result, err := client.DatabaseMigrationStatus(ctx, &scheddpb.DatabaseRef{DatabaseId: resourceUUID(resourceID)})
	return migrationProgress(result, err)
}

func migrationProgress(result *vmmdpb.DatabaseMigrationProgress, err error) (api.ManagedPostgresMigrationProgress, error) {
	if status.Code(err) == codes.Unimplemented {
		return api.ManagedPostgresMigrationProgress{}, managedpostgres.ErrUnsupported
	}
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, providerError(err)
	}
	switch result.GetState() {
	case dbmigration.None, dbmigration.Preparing, dbmigration.Copying, dbmigration.CatchingUp, dbmigration.Live,
		dbmigration.Paused, dbmigration.ResyncRequired, dbmigration.Stopped, dbmigration.Activated:
	default:
		return api.ManagedPostgresMigrationProgress{}, managedpostgres.ErrUnavailable
	}
	out := api.ManagedPostgresMigrationProgress{
		OperationID: result.GetOperationId(), State: result.GetState(), Reason: result.GetReason(),
		Tables: int(result.GetTables()), TablesReady: int(result.GetTablesReady()),
		ApplyLagBytes: result.GetApplyLagBytes(), RetainedWALBytes: result.GetRetainedWalBytes(),
	}
	if prep := result.GetPreparation(); prep != nil {
		out.Preparation = &api.ManagedPostgresMigrationPreparation{
			State: prep.GetState(), Reason: prep.GetReason(), IndexesTotal: int(prep.GetIndexesTotal()),
			IndexesReady: int(prep.GetIndexesReady()), Sequences: prep.GetSequences(),
		}
		for _, blocker := range prep.GetBlockers() {
			out.Preparation.Blockers = append(out.Preparation.Blockers, api.ManagedPostgresMigrationPreparationBlocker{
				Code: blocker.GetCode(), Count: blocker.GetCount(),
			})
		}
	}
	if verified := result.GetVerification(); verified != nil {
		out.Verification = &api.ManagedPostgresMigrationVerification{
			ID: verified.GetVerificationId(), State: verified.GetState(), Reason: verified.GetReason(),
			AppliedLSN: verified.GetAppliedLsn(), SequencesSynced: int(verified.GetSequencesSynced()),
		}
		if at := verified.GetVerifiedAt(); at != nil {
			out.Verification.VerifiedAt = at.AsTime()
		}
	}
	return out, nil
}
