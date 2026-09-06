package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
	"github.com/onebox-faas/faas/pkg/state"
)

func loadManagedPostgres(pool *pgxpool.Pool, getenv func(string) string, log *slog.Logger) (*managedpostgres.Service, *managedpostgres.Reconciler, *managedpostgres.BindingService, *managedpostgres.BindingReconciler, *managedpostgres.UsageCollector, error) {
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		return nil, nil, nil, nil, nil, err
	}
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	usageCollector, err := managedpostgres.NewUsageCollector(registry, store, managedpostgres.UsageCollectorOptions{Logger: log})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{
		ProvisioningEnabled: func() bool { return registry.ProvisioningEnabled },
		Admit: func(ctx context.Context, accountID string) error {
			return registry.UsagePolicy().Admit(ctx, store, accountID, time.Now().UTC())
		},
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	reconciler, err := managedpostgres.NewReconciler(service, managedpostgres.ReconcilerOptions{
		IncludeProvisioning: func() bool { return registry.ProvisioningEnabled },
		Logger:              log,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	secretSink, err := newAppSecretCredentialSink(
		state.NewPgStore(pool),
		func() *age.X25519Recipient {
			if setSecretRecipient == nil {
				return nil
			}
			return setSecretRecipient()
		},
		func() []byte {
			if hostHMACKey == nil {
				return nil
			}
			return hostHMACKey()
		},
	)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	bindingService, err := managedpostgres.NewBindingService(registry, store, store, secretSink, managedpostgres.BindingServiceOptions{
		ProvisioningEnabled: func() bool { return registry.ProvisioningEnabled },
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	bindingReconciler, err := managedpostgres.NewBindingReconciler(bindingService, managedpostgres.BindingReconcilerOptions{
		IncludeProvisioning: func() bool { return registry.ProvisioningEnabled },
		Logger:              log,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return service, reconciler, bindingService, bindingReconciler, usageCollector, nil
}

func (s *server) WithManagedPostgres(service *managedpostgres.Service, reconciler *managedpostgres.Reconciler, bindingService *managedpostgres.BindingService, bindingReconciler *managedpostgres.BindingReconciler, usageCollector *managedpostgres.UsageCollector) *server {
	s.managedPostgres = service
	s.managedPostgresReconciler = reconciler
	s.managedPostgresBindings = bindingService
	s.managedPostgresBindingReconciler = bindingReconciler
	s.managedPostgresUsageCollector = usageCollector
	return s
}

func (s *server) runManagedPostgresUsageCollector(ctx context.Context) {
	if s.managedPostgresUsageCollector == nil {
		return
	}
	if err := s.managedPostgresUsageCollector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("managed postgres usage collector exited", "error", err)
	}
}

func (s *server) runManagedPostgresReconciler(ctx context.Context) {
	if s.managedPostgresReconciler == nil {
		return
	}
	if err := s.managedPostgresReconciler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("managed postgres reconciler exited", "error", err)
	}
}

func (s *server) runManagedPostgresBindingReconciler(ctx context.Context) {
	if s.managedPostgresBindingReconciler == nil {
		return
	}
	if err := s.managedPostgresBindingReconciler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("managed postgres binding reconciler exited", "error", err)
	}
}
