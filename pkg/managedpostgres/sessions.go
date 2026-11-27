package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
)

type nativeQueryControls interface {
	ListSessions(context.Context, string) (dbguest.Sessions, error)
	CancelQuery(context.Context, string, string) (string, error)
}

func (s *Service) queryControls(ctx context.Context, accountID, id string) (Database, nativeQueryControls, error) {
	d, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return d, nil, err
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return d, nil, err
	}
	native, ok := backend.Provider.(nativeQueryControls)
	if !ok || backend.Driver != "firecracker" {
		return d, nil, ErrUnsupported
	}
	return d, native, nil
}

func (s *Service) ListSessions(ctx context.Context, accountID, id string) (api.ManagedPostgresSessions, error) {
	d, native, err := s.queryControls(ctx, accountID, id)
	if err != nil {
		return api.ManagedPostgresSessions{}, err
	}
	out := api.ManagedPostgresSessions{DatabaseID: d.ID}
	if d.State != StateReady || d.ProviderResourceID == "" {
		out.State = dbguest.DiagnosticsUnavailable
		return out, nil
	}
	out.Sessions, err = native.ListSessions(ctx, d.ProviderResourceID)
	return out, err
}

func (s *Service) CancelQuery(ctx context.Context, accountID, id, targetID string) (api.ManagedPostgresQueryCancellation, error) {
	d, native, err := s.queryControls(ctx, accountID, id)
	if err != nil {
		return api.ManagedPostgresQueryCancellation{}, err
	}
	if !dbquerycontrol.ValidID(targetID) {
		return api.ManagedPostgresQueryCancellation{}, ErrInvalid
	}
	out := api.ManagedPostgresQueryCancellation{DatabaseID: d.ID, TargetID: targetID}
	if d.State != StateReady || d.ProviderResourceID == "" {
		out.Outcome = dbquerycontrol.Stale
		return out, nil
	}
	out.Outcome, err = native.CancelQuery(ctx, d.ProviderResourceID, targetID)
	return out, err
}
