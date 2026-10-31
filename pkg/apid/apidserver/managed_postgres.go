package apidserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresOverageGate decides whether an account may run its databases
// past the compute allowance (plan and capability); nil means never.
type managedPostgresOverageGate func(state.Account) bool

// managedPostgresRelease decides production provisioning for one account
// (ADR-20261028103115); nil releases every account, for workers that only
// continue work apid already admitted.
type managedPostgresRelease func(context.Context, string) bool

// managedPostgresHooks connects the managed PostgreSQL loader to the rest of
// apid. Workers pass nil: no overage, every account released, and no cap email.
type managedPostgresHooks struct {
	Overage  managedPostgresOverageGate
	Released managedPostgresRelease
	// NotifyCap queues a compute cap email (ADR-20261025094915 decision 8).
	NotifyCap func(context.Context, managedpostgres.CapEvent) error
	// BillingPolicy reads the billing provider's managed PostgreSQL overage
	// policy (ADR-20261031165104); nil or ok=false is off.
	BillingPolicy func() (managedpostgres.BillingPolicy, bool)
}

func (h *managedPostgresHooks) billingPolicy() func() (managedpostgres.BillingPolicy, bool) {
	if h == nil {
		return nil
	}
	return h.BillingPolicy
}

func (h *managedPostgresHooks) overage() managedPostgresOverageGate {
	if h == nil {
		return nil
	}
	return h.Overage
}

func (h *managedPostgresHooks) released() managedPostgresRelease {
	if h == nil {
		return nil
	}
	return h.Released
}

func (h *managedPostgresHooks) notifyCap() func(context.Context, managedpostgres.CapEvent) error {
	if h == nil {
		return nil
	}
	return h.NotifyCap
}

func loadManagedPostgres(pool *pgxpool.Pool, getenv func(string) string, log *slog.Logger, hooks *managedPostgresHooks, registerers ...prometheus.Registerer) (*managedpostgres.Service, *managedpostgres.Reconciler, *managedpostgres.BindingService, *managedpostgres.BindingReconciler, *managedpostgres.UsageCollector, *managedpostgres.HealthCollector, error) {
	registry, err := managedpostgres.Load(getenv, managedPostgresFactories())
	if err != nil || registry == nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	accountStore := state.NewPgStore(pool)
	var metrics *managedpostgres.Metrics
	if len(registerers) > 0 {
		metrics, err = managedpostgres.NewMetrics(registerers[0], "apid", registry.UsagePolicy().Enabled)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
	}
	usageOptions := managedpostgres.UsageCollectorOptions{Logger: log, Exclusive: store.TryAcquireUsageSweep}
	if metrics != nil {
		usageOptions.Observe = metrics.ObserveUsage
		usageOptions.ObserveSweep = metrics.ObserveUsageSweep
	}
	overage, released := hooks.overage(), hooks.released()
	capEnforcer, err := loadManagedPostgresCapEnforcer(registry, store, accountStore, getenv, log, hooks, registerers...)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	billingSweeper, err := loadManagedPostgresBillingSweeper(registry, store, accountStore, log, hooks, registerers...)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	var fleet *managedpostgres.Service
	usageOptions.AfterSweep = func(ctx context.Context) {
		if _, err := capEnforcer.Enforce(ctx); err != nil && ctx.Err() == nil {
			log.Warn("managed postgres compute cap sweep failed", "error", err)
		}
		if billingSweeper != nil {
			if _, err := billingSweeper.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Warn("managed postgres overage billing sweep failed", "error", err)
			}
		}
		observeManagedPostgresFleet(ctx, fleet, metrics, log)
	}
	usageCollector, err := managedpostgres.NewUsageCollector(registry, store, usageOptions)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	baseProvisioningGate := managedpostgres.NewProvisioningGate(registry, getenv, time.Now)
	production := managedpostgres.ProductionEnvironmentActive(getenv)
	if production {
		logManagedPostgresProductionReadiness(registry, getenv, metrics, log)
	}
	provisioningGate := func() bool {
		enabled := baseProvisioningGate()
		if metrics != nil {
			metrics.SetProvisioningEnabled(enabled)
		}
		return enabled
	}
	canaryAccountGate := managedpostgres.NewStagingCanaryAccountGate(getenv)
	provisioningAllowed := func(ctx context.Context, accountID string) bool {
		// Production releases accounts by the capability flag, staging by
		// its canary allowlist.
		allowed := canaryAccountGate(accountID)
		if production {
			allowed = released == nil || released(ctx, accountID)
		}
		if metrics != nil {
			metrics.ObserveCanary(allowed)
		}
		return allowed
	}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{
		ProvisioningEnabled: provisioningGate,
		ProvisioningAllowed: provisioningAllowed,
		MaxDatabasesPerAccount: func(ctx context.Context, accountID string) (int, error) {
			account, err := state.BillingResourceAccount(ctx, accountStore, accountID)
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
		AdmitResize: func(ctx context.Context, accountID string, target managedpostgres.Spec) error {
			account, err := state.BillingResourceAccount(ctx, accountStore, accountID)
			if err != nil {
				return err
			}
			limits, ok := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
			if !ok || limits.DatabasesMax <= 0 || !managedPostgresPlanAllows(limits, target) || (!target.ScaleToZero && !limits.AlwaysOnAllowed) {
				return managedpostgres.ErrNotInPlan
			}
			return nil
		},
		Admit: func(ctx context.Context, accountID string) error {
			if !registry.UsagePolicy().Enabled {
				return nil
			}
			account, lookupErr := state.BillingResourceAccount(ctx, accountStore, accountID)
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
			overageOn, overageErr := managedPostgresStoredOverage(ctx, store, overage, account, accountID)
			if overageErr != nil {
				return overageErr
			}
			admitErr := registry.UsagePolicy().AdmitWithCeilings(ctx, store, accountID, time.Now().UTC(), managedPostgresUsageCeilings(limits, overageOn, managedPostgresOverageBilled(hooks.billingPolicy())))
			if metrics != nil {
				metrics.ObserveAdmission(admitErr)
			}
			return admitErr
		},
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	fleet = service
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
		return nil, nil, nil, nil, nil, nil, err
	}
	secretSink, err := newAppSecretCredentialSink(
		accountStore,
		func() *age.X25519Recipient {
			if setSecretRecipient == nil {
				return nil
			}
			return runtimeSecretRecipient()
		},
		hostCredentialHMACKey,
	)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	secretSink.identities = hostCredentialIdentities
	secretSink.probe = managedpostgres.VerifyCredentialSQL
	bindingService, err := managedpostgres.NewBindingService(registry, store, store, secretSink, managedpostgres.BindingServiceOptions{
		ProvisioningEnabled: provisioningGate,
		ProvisioningAllowed: provisioningAllowed,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
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
		return nil, nil, nil, nil, nil, nil, err
	}
	healthOptions := managedpostgres.HealthCollectorOptions{Logger: log}
	if len(registerers) > 0 {
		healthMetrics, metricsErr := managedpostgres.NewHealthMetrics(registerers[0], "apid", registry.HealthPolicy())
		if metricsErr != nil {
			return nil, nil, nil, nil, nil, nil, metricsErr
		}
		healthOptions.Observe, healthOptions.ObserveSweep = healthMetrics.Observe, healthMetrics.ObserveSweep
	}
	healthCollector, err := managedpostgres.NewHealthCollector(registry, store, healthOptions)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	return service, reconciler, bindingService, bindingReconciler, usageCollector, healthCollector, nil
}

// managedPostgresUsageCeilings converts customer-facing entitlements into the
// canonical meters: the account's pooled storage held for a whole billing
// month, and its monthly compute units (ADR-1430). Restore history, egress and
// spend remain operator-configured COGS ceilings; the intersection is applied
// in managedpostgres so adapters never learn about Gregale plan names.
// hostCredentialHMACKey and hostCredentialIdentities are the host
// secret providers shared by the credential sink and candidate verification.
func hostCredentialHMACKey() []byte {
	if hostHMACKey == nil {
		return nil
	}
	return hostHMACKeyBytes()
}

func hostCredentialIdentities() []*age.X25519Identity {
	if mfaIdentities != nil {
		if ids := mfaIdentities(); len(ids) > 0 {
			return ids
		}
	}
	if mfaIdentity != nil {
		if id := mfaIdentity(); id != nil {
			return []*age.X25519Identity{id}
		}
	}
	return nil
}

// loadManagedPostgresCapEnforcer builds the compute cap enforcer
// (ADR-20261025094915). Each subject's ceiling is its plan's monthly compute
// allowance, or twice it with overage on; the enforcer intersects it with the
// operator's usage cap.
func loadManagedPostgresCapEnforcer(registry *managedpostgres.Registry, store *managedpostgres.PostgresStore, accountStore state.AppOwnerReader, getenv func(string) string, log *slog.Logger, hooks *managedPostgresHooks, registerers ...prometheus.Registerer) (*managedpostgres.CapEnforcer, error) {
	overage := hooks.overage()
	mode, err := managedpostgres.ParseCapEnforcementMode(getenv(managedpostgres.CapEnforcementEnv))
	if err != nil {
		return nil, err
	}
	storageMode, err := managedpostgres.ParseStorageCapEnforcementMode(getenv(managedpostgres.StorageCapEnforcementEnv))
	if err != nil {
		return nil, err
	}
	options := managedpostgres.CapEnforcerOptions{
		Mode:        mode,
		StorageMode: storageMode,
		Logger:      log,
		Notify:      hooks.notifyCap(),
		Ceiling: func(ctx context.Context, subject string) (managedpostgres.CapLimits, error) {
			account, err := state.BillingResourceAccount(ctx, accountStore, subject)
			if err != nil {
				return managedpostgres.CapLimits{}, err
			}
			limits, _ := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
			overageOn, err := managedPostgresStoredOverage(ctx, store, overage, account, subject)
			if err != nil {
				return managedpostgres.CapLimits{}, err
			}
			billed := managedPostgresOverageBilled(hooks.billingPolicy())
			capLimits := managedpostgres.CapLimits{Allowance: limits.MonthlyComputeUnitSeconds,
				Ceiling: managedPostgresComputeCeiling(limits, overageOn, billed), Overage: overageOn,
				StoragePool: managedPostgresStoragePool(limits, overageOn)}
			if billed {
				if capLimits.SpendCapReached, err = managedPostgresSpendCapReached(ctx, accountStore, subject); err != nil {
					return managedpostgres.CapLimits{}, err
				}
			}
			return capLimits, nil
		},
	}
	if mode == managedpostgres.CapEnforcementOn {
		qualification, err := managedpostgres.LoadComputeCapQualification(getenv(managedpostgres.CapQualificationPathEnv))
		if err != nil {
			return nil, fmt.Errorf("%s=on needs a readable %s: %w", managedpostgres.CapEnforcementEnv, managedpostgres.CapQualificationPathEnv, err)
		}
		options.Qualification = &qualification
	}
	if storageMode == managedpostgres.CapEnforcementOn {
		qualification, err := managedpostgres.LoadStorageCapQualification(getenv(managedpostgres.StorageCapQualificationPathEnv))
		if err != nil {
			return nil, fmt.Errorf("%s=on needs a readable %s: %w", managedpostgres.StorageCapEnforcementEnv, managedpostgres.StorageCapQualificationPathEnv, err)
		}
		options.StorageQualification = &qualification
	}
	if len(registerers) > 0 {
		metrics, err := managedpostgres.NewCapMetrics(registerers[0], "apid", mode, storageMode)
		if err != nil {
			return nil, err
		}
		options.Observe = metrics.Observe
	}
	return managedpostgres.NewCapEnforcer(registry, store, options)
}

// configureManagedPostgresCapMode records the compute cap enforcement mode for
// the usage views. loadManagedPostgres already refused an invalid value.
func (s *server) configureManagedPostgresCapMode(getenv func(string) string) {
	s.managedPostgresCapMode, _ = managedpostgres.ParseCapEnforcementMode(getenv(managedpostgres.CapEnforcementEnv))
}

// managedPostgresStoredOverage reads a subject's overage choice when the
// account may use overage at all.
// loadManagedPostgresBillingSweeper builds the overage billing sweep
// (ADR-20261031165104). A subject is billable on a paid plan with overage on;
// it is priced against the plan's monthly compute allowance and storage pool.
func loadManagedPostgresBillingSweeper(registry *managedpostgres.Registry, store *managedpostgres.PostgresStore, accountStore state.AppOwnerReader, log *slog.Logger, hooks *managedPostgresHooks, registerers ...prometheus.Registerer) (*managedpostgres.BillingSweeper, error) {
	policy := hooks.billingPolicy()
	if policy == nil {
		return nil, nil
	}
	overage := hooks.overage()
	options := managedpostgres.BillingSweeperOptions{
		Policy: policy,
		Logger: log,
		Entitlement: func(ctx context.Context, subject string) (managedpostgres.OverageEntitlement, error) {
			account, err := state.BillingResourceAccount(ctx, accountStore, subject)
			if err != nil {
				return managedpostgres.OverageEntitlement{}, err
			}
			limits, ok := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
			if !api.Plan(account.Plan).IsPaid() || !ok || limits.DatabasesMax <= 0 {
				return managedpostgres.OverageEntitlement{}, nil
			}
			on, err := managedPostgresStoredOverage(ctx, store, overage, account, subject)
			if err != nil || !on {
				return managedpostgres.OverageEntitlement{}, err
			}
			return managedpostgres.OverageEntitlement{Billable: true, ComputeAllowanceSeconds: limits.MonthlyComputeUnitSeconds,
				StoragePoolBytes: limits.StorageLimitBytes}, nil
		},
	}
	if len(registerers) > 0 {
		metrics, err := managedpostgres.NewBillingMetrics(registerers[0], "apid")
		if err != nil {
			return nil, err
		}
		options.Observe = metrics.Observe
	}
	return managedpostgres.NewBillingSweeper(registry, store, options)
}

// managedPostgresSpendCapReached reports a spend-cap hold that still applies
// to subject's current cap this month (ADR-20261026120843): once managed
// PostgreSQL overage is billed, its databases stop with the apps at the cap
// (ADR-20261031165104 decision 4).
func managedPostgresSpendCapReached(ctx context.Context, store any, subject string) (bool, error) {
	caps, ok := store.(interface {
		GetAccountOverageCapCents(context.Context, string) (int64, bool, error)
	})
	holds, holdsOK := store.(state.SpendCapHoldStore)
	if !ok || !holdsOK {
		return false, nil
	}
	capCents, capped, err := caps.GetAccountOverageCapCents(ctx, subject)
	if err != nil || !capped {
		return false, err
	}
	hold, found, err := holds.GetSpendCapHold(ctx, subject)
	if err != nil || !found {
		return false, err
	}
	return hold.Holds(capCents, time.Now()), nil
}

func managedPostgresStoredOverage(ctx context.Context, store managedpostgres.OverageStore, overage managedPostgresOverageGate, account state.Account, subject string) (bool, error) {
	if overage == nil || !overage(account) {
		return false, nil
	}
	return store.OverageEnabled(ctx, subject)
}

func managedPostgresUsageCeilings(limits api.ManagedPostgresPlanLimits, overage, billed bool) managedpostgres.UsageCeilings {
	if limits.DatabasesMax <= 0 {
		return managedpostgres.UsageCeilings{}
	}
	const secondsPerBillingMonth = int64(31 * 24 * time.Hour / time.Second)
	ceilings := managedpostgres.UsageCeilings{}
	if limits.MonthlyComputeUnitSeconds > 0 {
		ceilings.MaxMonthlyComputeUnitSeconds = managedPostgresComputeCeiling(limits, overage, billed)
	}
	if limits.StorageLimitBytes > 0 && limits.StorageLimitBytes <= math.MaxInt64/secondsPerBillingMonth {
		ceilings.MaxMonthlyStorageByteSeconds = limits.StorageLimitBytes * secondsPerBillingMonth
	}
	return ceilings
}

func (s *server) WithManagedPostgres(service *managedpostgres.Service, reconciler *managedpostgres.Reconciler, bindingService *managedpostgres.BindingService, bindingReconciler *managedpostgres.BindingReconciler, usageCollector *managedpostgres.UsageCollector, healthCollector *managedpostgres.HealthCollector) *server {
	s.managedPostgres = service
	s.managedPostgresReconciler = reconciler
	s.managedPostgresBindings = bindingService
	s.managedPostgresBindingReconciler = bindingReconciler
	s.managedPostgresCutovers = bindingReconciler.CutoverService()
	s.managedPostgresUsageCollector = usageCollector
	s.managedPostgresHealthCollector = healthCollector
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

// managedPostgresBillingPolicy reads the configured billing provider's
// managed PostgreSQL overage policy (ADR-20261031165104). The provider is set
// after managed PostgreSQL loads, so the sweep reads it on every run.
func (s *server) managedPostgresBillingPolicy() (managedpostgres.BillingPolicy, bool) {
	source, ok := s.billingProvider.(billing.ManagedPostgresBillingPolicySource)
	if !ok {
		return managedpostgres.BillingPolicy{}, false
	}
	provider, mode, from, enabled := source.ManagedPostgresBillingPolicy()
	if !enabled {
		return managedpostgres.BillingPolicy{}, false
	}
	return managedpostgres.BillingPolicy{Provider: provider, Mode: mode, EffectiveFrom: from}, true
}
