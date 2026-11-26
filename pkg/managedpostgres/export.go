package managedpostgres

import (
	"context"
	"io"

	"github.com/onebox-faas/faas/pkg/pgexport"
)

type logicalExporter interface {
	ExportDatabase(context.Context, string, int, io.Writer) (pgexport.Source, error)
}

func (s *Service) ExportSource(ctx context.Context, account, id string) (Database, error) {
	source, _, err := s.exportSource(ctx, account, id)
	return source, err
}

func (s *Service) exportSource(ctx context.Context, account, id string) (Database, logicalExporter, error) {
	source, err := customerDatabase(ctx, s.store, account, id)
	if err != nil {
		return Database{}, nil, err
	}
	if source.State != StateReady || source.ProviderResourceID == "" || source.DesiredGeneration != source.ObservedGeneration {
		return Database{}, nil, ErrConflict
	}
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return Database{}, nil, err
	}
	exporter, ok := backend.Provider.(logicalExporter)
	if !ok || backend.Driver != "firecracker" {
		return Database{}, nil, ErrUnsupported
	}
	return source, exporter, nil
}

func (s *Service) ExportDatabase(ctx context.Context, account, id string, generation int64, dst io.Writer) (pgexport.Source, error) {
	database, exporter, err := s.exportSource(ctx, account, id)
	if err != nil {
		return pgexport.Source{}, err
	}
	if database.ObservedGeneration != generation || database.OrgID != Organization(ctx) {
		return pgexport.Source{}, ErrConflict
	}
	source, err := exporter.ExportDatabase(ctx, database.ProviderResourceID, database.Spec.PostgresMajor, dst)
	if err != nil {
		return pgexport.Source{}, err
	}
	current, err := s.ExportSource(ctx, account, id)
	if err != nil {
		return pgexport.Source{}, err
	}
	if !source.Valid() || source.Major != database.Spec.PostgresMajor || current.ObservedGeneration != generation ||
		current.OrgID != database.OrgID || current.ProviderResourceID != database.ProviderResourceID || current.DataResourceID != database.DataResourceID {
		return pgexport.Source{}, ErrConflict
	}
	return source, nil
}
