package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
	"github.com/onebox-faas/faas/pkg/state"
)

func loadManagedPostgres(pool *pgxpool.Pool, getenv func(string) string, log *slog.Logger, registerers ...prometheus.Registerer) (*managedpostgres.Service, *managedpostgres.Reconciler, *managedpostgres.BindingService, *managedpostgres.BindingReconciler, *managedpostgres.UsageCollector, error) {
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		return nil, nil, nil, nil, nil, err
	}
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	accountStore := state.NewPgStore(pool)
	var metrics *managedpostgres.Metrics
	if len(registerers) > 0 {
		metrics, err = managedpostgres.NewMetrics(registerers[0], "apid", registry.UsagePolicy().Enabled)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}
	usageOptions := managedpostgres.UsageCollectorOptions{Logger: log}
	if metrics != nil {
		usageOptions.Observe = metrics.ObserveUsage
		usageOptions.ObserveSweep = metrics.ObserveUsageSweep
	}
	usageCollector, err := managedpostgres.NewUsageCollector(registry, store, usageOptions)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	baseProvisioningGate := managedpostgres.NewStagingProvisioningGate(registry, getenv, time.Now)
	provisioningGate := func() bool {
		enabled := baseProvisioningGate()
		if metrics != nil {
			metrics.SetProvisioningEnabled(enabled)
		}
		return enabled
	}
	canaryAccountGate := managedpostgres.NewStagingCanaryAccountGate(getenv)
	provisioningAllowed := func(ctx context.Context, accountID string) bool {
		allowed := canaryAccountGate(accountID)
		if metrics != nil {
			metrics.ObserveCanary(allowed)
		}
		return allowed
	}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{
		ProvisioningEnabled: provisioningGate,
		ProvisioningAllowed: provisioningAllowed,
		MaxDatabasesPerAccount: func(ctx context.Context, accountID string) (int, error) {
			account, err := accountStore.AccountByID(ctx, accountID)
			if err != nil {
				return 0, err
			}
			limits, ok := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
			if !ok || limits.DatabasesMax <= 0 {
				return 0, managedpostgres.ErrQuotaExceeded
			}
			limit := limits.DatabasesMax
			if registry.MaxDatabasesPerAccount < limit {
				limit = registry.MaxDatabasesPerAccount
			}
			return limit, nil
		},
		Admit: func(ctx context.Context, accountID string) error {
			if !registry.UsagePolicy().Enabled {
				return nil
			}
			account, lookupErr := accountStore.AccountByID(ctx, accountID)
			if lookupErr != nil {
				if metrics != nil {
					metrics.ObserveAdmission(lookupErr)
				}
				return lookupErr
			}
			limits, ok := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
			if !ok || limits.DatabasesMax <= 0 {
				admitErr := managedpostgres.ErrQuotaExceeded
				if metrics != nil {
					metrics.ObserveAdmission(admitErr)
				}
				return admitErr
			}
			admitErr := registry.UsagePolicy().AdmitWithCeilings(ctx, store, accountID, time.Now().UTC(), managedPostgresUsageCeilings(limits))
			if metrics != nil {
				metrics.ObserveAdmission(admitErr)
			}
			return admitErr
		},
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	reconciler, err := managedpostgres.NewReconciler(service, managedpostgres.ReconcilerOptions{
		IncludeProvisioning: provisioningGate,
		Observe: func(ob managedpostgres.ReconcileObservation) {
			if metrics != nil {
				metrics.ObserveReconcile(ob)
			}
		},
		Logger: log,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	secretSink, err := newAppSecretCredentialSink(
		accountStore,
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
		ProvisioningEnabled: provisioningGate,
		ProvisioningAllowed: provisioningAllowed,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	bindingReconciler, err := managedpostgres.NewBindingReconciler(bindingService, managedpostgres.BindingReconcilerOptions{
		IncludeProvisioning: provisioningGate,
		Observe: func(ob managedpostgres.BindingReconcileObservation) {
			if metrics != nil {
				metrics.ObserveBindingReconcile(ob)
			}
		},
		Logger: log,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return service, reconciler, bindingService, bindingReconciler, usageCollector, nil
}

// managedPostgresUsageCeilings converts customer-facing storage entitlements
// into the canonical byte-second meter. Compute, restore-history, egress, and
// spend remain operator-configured COGS ceilings; the intersection is applied
// in managedpostgres so adapters never learn about Gregale plan names.
func managedPostgresUsageCeilings(limits api.ManagedPostgresPlanLimits) managedpostgres.UsageCeilings {
	if limits.StorageLimitBytes <= 0 || limits.DatabasesMax <= 0 {
		return managedpostgres.UsageCeilings{}
	}
	const secondsPerBillingMonth = int64(31 * 24 * time.Hour / time.Second)
	accountStorageBytes := limits.StorageLimitBytes
	if accountStorageBytes > math.MaxInt64/int64(limits.DatabasesMax) {
		return managedpostgres.UsageCeilings{}
	}
	accountStorageBytes *= int64(limits.DatabasesMax)
	if accountStorageBytes > math.MaxInt64/secondsPerBillingMonth {
		return managedpostgres.UsageCeilings{}
	}
	return managedpostgres.UsageCeilings{MaxMonthlyStorageByteSeconds: accountStorageBytes * secondsPerBillingMonth}
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
