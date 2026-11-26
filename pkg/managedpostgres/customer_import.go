package managedpostgres

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// CustomerImporter supports a compatibility check and a copy through the
// customer import path. The existing Importer also serves internal cutovers.
type CustomerImporter interface {
	CustomerImport(context.Context, ImportRequest, bool) (ImportStatus, error)
	ImportStatus(context.Context, string) (ImportStatus, error)
}

func (s *Service) StartCustomerImport(ctx context.Context, accountID, id string, request api.ManagedPostgresImportRequest, checkOnly bool) (api.ManagedPostgresImportStatus, error) {
	if !checkOnly && !validResizeRequestID(request.OperationID) {
		return api.ManagedPostgresImportStatus{}, fmt.Errorf("%w: operation_id must be a lowercase UUID", ErrInvalid)
	}
	target, importer, err := s.customerImportTarget(ctx, accountID, id)
	if err != nil {
		return api.ManagedPostgresImportStatus{}, err
	}
	if target.State != StateReady {
		return api.ManagedPostgresImportStatus{}, fmt.Errorf("%w: the import target must be ready", ErrConflict)
	}
	src := request.Source
	op := request.OperationID
	if checkOnly {
		op = target.ID // The guest validates the request ID even for a probe.
	}
	progress, err := importer.CustomerImport(ctx, ImportRequest{
		ResourceID: target.ProviderResourceID, OperationID: op,
		Source: ImportSource{Host: src.Host, Port: src.Port, Database: src.Database,
			User: src.User, Password: src.Password, SkipExtensions: src.SkipExtensions},
	}, checkOnly)
	if err != nil {
		return api.ManagedPostgresImportStatus{}, err
	}
	result := customerImportView(progress)
	if checkOnly {
		result.OperationID = ""
		if progress.State != ImportChecked && progress.State != ImportFailed {
			return api.ManagedPostgresImportStatus{}, ErrUnavailable
		}
	} else if progress.OperationID != op || (progress.State != ImportRunning && progress.State != ImportDone && progress.State != ImportFailed) {
		return api.ManagedPostgresImportStatus{}, ErrUnavailable
	}
	return result, nil
}

func (s *Service) CustomerImportStatus(ctx context.Context, accountID, id, operation string) (api.ManagedPostgresImportStatus, error) {
	if !validResizeRequestID(operation) {
		return api.ManagedPostgresImportStatus{}, fmt.Errorf("%w: operation ID must be a lowercase UUID", ErrInvalid)
	}
	target, importer, err := s.customerImportTarget(ctx, accountID, id)
	if err != nil {
		return api.ManagedPostgresImportStatus{}, err
	}
	progress, err := importer.ImportStatus(ctx, target.ProviderResourceID)
	if err != nil {
		return api.ManagedPostgresImportStatus{}, err
	}
	if progress.OperationID != operation || progress.State == ImportNone {
		return api.ManagedPostgresImportStatus{State: "unverified", ErrorCode: "import_status_unavailable", OperationID: operation}, nil
	}
	return customerImportView(progress), nil
}

func (s *Service) customerImportTarget(ctx context.Context, accountID, id string) (Database, CustomerImporter, error) {
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return Database{}, nil, err
	}
	if database.ProviderResourceID == "" {
		return Database{}, nil, fmt.Errorf("%w: the import target has not been provisioned", ErrConflict)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Database{}, nil, err
	}
	importer, ok := backend.Provider.(CustomerImporter)
	if !ok || backend.Driver != "firecracker" {
		return Database{}, nil, ErrUnsupported
	}
	return database, importer, nil
}

func customerImportView(s ImportStatus) api.ManagedPostgresImportStatus {
	return api.ManagedPostgresImportStatus{
		State: string(s.State), ErrorCode: s.ErrorCode, OperationID: s.OperationID,
		SourceMajor: s.SourceMajor, MissingExtensions: s.MissingExtensions, RequiredVolumeBytes: s.RequiredVolumeBytes,
		IncompatibleExtensions: extensionVersions(s.IncompatibleExtensions),
		StartedAt:              s.StartedAt, FinishedAt: s.FinishedAt,
	}
}
