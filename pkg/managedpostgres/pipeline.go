package managedpostgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

// PipelineSQLProvider is an optional internal provider operation. It never
// creates an application credential or returns provider credentials to callers.
// Only the isolated pipeline worker invokes this capability.
type PipelineSQLProvider interface {
	WithPipelineSQL(context.Context, Database, func(*pgx.Conn, string) error) error
}

// BoundedCredentialProvider must enforce and verify the login connection limit
// before returning credentials. Generic providers must not silently ignore it.
type BoundedCredentialProvider interface {
	IssueBoundedCredentials(context.Context, CredentialRequest) (CredentialMaterial, error)
}

func (s *Service) PipelineCapacityTarget(ctx context.Context, account, id string) (connectioncapacity.Target, error) {
	d, _, err := s.pipelineTarget(ctx, account, id)
	return connectioncapacity.Target{AccountID: account, DatabaseID: id, Generation: d.ObservedGeneration, BackendFingerprint: d.BackendFingerprint, DataResourceID: d.DataResourceID}, err
}

func (s *Service) ValidatePipelineTarget(ctx context.Context, account, id string) error {
	_, _, err := s.pipelineTarget(ctx, account, id)
	return err
}
func (s *Service) pipelineTarget(ctx context.Context, account, id string) (Database, PipelineSQLProvider, error) {
	if s == nil || s.registry == nil {
		return Database{}, nil, ErrUnavailable
	}
	d, err := customerDatabase(ctx, s.store, account, id)
	if err != nil {
		return Database{}, nil, err
	}
	if d.State != StateReady || d.DesiredGeneration != d.ObservedGeneration || d.ProviderResourceID == "" || d.DataResourceID == "" {
		return Database{}, nil, ErrConflict
	}
	b, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return Database{}, nil, err
	}
	provider, ok := b.Provider.(PipelineSQLProvider)
	if !ok {
		return Database{}, nil, ErrUnsupported
	}
	if _, ok := b.Provider.(BoundedCredentialProvider); !ok {
		return Database{}, nil, ErrUnsupported
	}
	if b.Capabilities.SupportsCredentialAccess(CredentialReadOnly) != nil {
		return Database{}, nil, ErrUnsupported
	}
	return d, provider, nil
}
func (s *Service) WithPipelineSQL(ctx context.Context, account, id string, run func(*pgx.Conn, string) error) error {
	d, p, err := s.pipelineTarget(ctx, account, id)
	if err != nil {
		return err
	}
	if run == nil {
		return ErrInvalid
	}
	return p.WithPipelineSQL(ctx, d, run)
}
