package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

func loadManagedPostgres(pool *pgxpool.Pool, getenv func(string) string, log *slog.Logger) (*managedpostgres.Service, *managedpostgres.Reconciler, error) {
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		return nil, nil, err
	}
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		return nil, nil, err
	}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{
		ProvisioningEnabled: func() bool { return registry.ProvisioningEnabled },
	})
	if err != nil {
		return nil, nil, err
	}
	reconciler, err := managedpostgres.NewReconciler(service, managedpostgres.ReconcilerOptions{
		IncludeProvisioning: func() bool { return registry.ProvisioningEnabled },
		Logger:              log,
	})
	if err != nil {
		return nil, nil, err
	}
	return service, reconciler, nil
}

func (s *server) WithManagedPostgres(service *managedpostgres.Service, reconciler *managedpostgres.Reconciler) *server {
	s.managedPostgres = service
	s.managedPostgresReconciler = reconciler
	return s
}

func (s *server) runManagedPostgresReconciler(ctx context.Context) {
	if s.managedPostgresReconciler == nil {
		return
	}
	if err := s.managedPostgresReconciler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("managed postgres reconciler exited", "error", err)
	}
}
