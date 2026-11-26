package managedpostgres

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) CheckCustomerMigration(ctx context.Context, accountID, id string, source api.ManagedPostgresImportSource) (api.ManagedPostgresMigrationCheck, error) {
	target, importer, err := s.customerImportTarget(ctx, accountID, id)
	if err != nil {
		return api.ManagedPostgresMigrationCheck{}, err
	}
	if target.State != StateReady {
		return api.ManagedPostgresMigrationCheck{}, fmt.Errorf("%w: the migration target must be ready", ErrConflict)
	}
	if len(source.SkipExtensions) != 0 {
		return api.ManagedPostgresMigrationCheck{}, fmt.Errorf("%w: migration checks inspect the full source", ErrInvalid)
	}
	checker, ok := importer.(interface {
		CheckMigration(context.Context, ImportRequest) (api.ManagedPostgresMigrationCheck, error)
	})
	if !ok {
		return api.ManagedPostgresMigrationCheck{}, ErrUnsupported
	}
	return checker.CheckMigration(ctx, ImportRequest{
		ResourceID: target.ProviderResourceID, OperationID: target.ID,
		Source: ImportSource{Host: source.Host, Port: source.Port, Database: source.Database, User: source.User, Password: source.Password},
	})
}

type customerMigrator interface {
	LiveMigration(context.Context, ImportRequest, bool) (api.ManagedPostgresMigrationProgress, error)
	MigrationStatus(context.Context, string) (api.ManagedPostgresMigrationProgress, error)
}

func (s *Service) LiveCustomerMigration(ctx context.Context, accountID, id string, request api.ManagedPostgresImportRequest, stop bool) (api.ManagedPostgresMigrationProgress, error) {
	importer, req, err := s.customerMigrationMutation(ctx, accountID, id, request, stop)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	migrator, ok := importer.(customerMigrator)
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, ErrUnsupported
	}
	return migrator.LiveMigration(ctx, req, stop)
}

func (s *Service) PrepareCustomerMigration(ctx context.Context, accountID, id string, request api.ManagedPostgresImportRequest) (api.ManagedPostgresMigrationProgress, error) {
	importer, req, err := s.customerMigrationMutation(ctx, accountID, id, request, false)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	migrator, ok := importer.(interface {
		PrepareMigration(context.Context, ImportRequest) (api.ManagedPostgresMigrationProgress, error)
	})
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, ErrUnsupported
	}
	return migrator.PrepareMigration(ctx, req)
}

func (s *Service) customerMigrationMutation(ctx context.Context, accountID, id string, request api.ManagedPostgresImportRequest, stop bool) (CustomerImporter, ImportRequest, error) {
	target, importer, err := s.customerImportTarget(ctx, accountID, id)
	if err != nil {
		return nil, ImportRequest{}, err
	}
	src := request.Source
	if !validResizeRequestID(request.OperationID) || len(src.SkipExtensions) != 0 {
		return nil, ImportRequest{}, fmt.Errorf("%w: migration requires an operation UUID and the full source", ErrInvalid)
	}
	if target.State != StateReady || (!stop && target.Spec.Availability != AvailabilitySingleZone) {
		return nil, ImportRequest{}, fmt.Errorf("%w: the migration requires a ready single-zone target", ErrConflict)
	}
	return importer, ImportRequest{
		ResourceID: target.ProviderResourceID, OperationID: request.OperationID,
		Source: ImportSource{Host: src.Host, Port: src.Port, Database: src.Database, User: src.User, Password: src.Password},
	}, nil
}

func (s *Service) VerifyCustomerMigration(ctx context.Context, accountID, id string, request api.ManagedPostgresMigrationVerifyRequest) (api.ManagedPostgresMigrationProgress, error) {
	if !validResizeRequestID(request.VerificationID) || !request.SourceWritesPaused {
		return api.ManagedPostgresMigrationProgress{}, fmt.Errorf("%w: provide a verification UUID and acknowledge paused source writes", ErrInvalid)
	}
	importer, req, err := s.customerMigrationMutation(ctx, accountID, id, request.ManagedPostgresImportRequest, false)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	migrator, ok := importer.(interface {
		VerifyMigration(context.Context, ImportRequest, string) (api.ManagedPostgresMigrationProgress, error)
	})
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, ErrUnsupported
	}
	return migrator.VerifyMigration(ctx, req, request.VerificationID)
}

func (s *Service) CustomerMigrationStatus(ctx context.Context, accountID, id string) (api.ManagedPostgresMigrationProgress, error) {
	target, importer, err := s.customerImportTarget(ctx, accountID, id)
	if err != nil {
		return api.ManagedPostgresMigrationProgress{}, err
	}
	migrator, ok := importer.(customerMigrator)
	if !ok {
		return api.ManagedPostgresMigrationProgress{}, ErrUnsupported
	}
	return migrator.MigrationStatus(ctx, target.ProviderResourceID)
}
