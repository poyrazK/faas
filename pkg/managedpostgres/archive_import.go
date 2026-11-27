package managedpostgres

import (
	"context"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pgarchive"
)

func (s *Service) ImportArchive(ctx context.Context, account, database, operation string, manifest pgarchive.Manifest, src io.Reader) (api.ManagedPostgresImportStatus, error) {
	if !validResizeRequestID(operation) || !manifest.Valid() || src == nil {
		return api.ManagedPostgresImportStatus{}, ErrInvalid
	}
	target, importer, err := s.customerImportTarget(ctx, account, database)
	if err != nil {
		return api.ManagedPostgresImportStatus{}, err
	}
	if target.State != StateReady || target.ObservedGeneration != target.DesiredGeneration {
		return api.ManagedPostgresImportStatus{}, ErrConflict
	}
	archiveImporter, ok := importer.(interface {
		ImportArchive(context.Context, string, string, pgarchive.Manifest, io.Reader) (ImportStatus, error)
	})
	if !ok {
		return api.ManagedPostgresImportStatus{}, ErrUnsupported
	}
	result, err := archiveImporter.ImportArchive(ctx, target.ProviderResourceID, operation, manifest, src)
	if err != nil {
		return api.ManagedPostgresImportStatus{}, err
	}
	return customerImportView(result), nil
}
