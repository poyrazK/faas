package apidserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/managedpg"
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
	// Charges returns the billing provider's managed PostgreSQL overage sink,
	// or nil (ADR-20261031110037). It is read at each sweep because apid
	// attaches the billing provider after loading managed PostgreSQL.
	Charges func() billing.ManagedPostgresChargeSink
	// RestoreSlotExpired records a copy whose delete was scheduled because
	// its restore slot ended without room (ADR-20261031123259).
	RestoreSlotExpired func(context.Context, managedpostgres.Database)
	// NotifyRestoreSlot queues a restore slot email (ADR-20261031123259).
	NotifyRestoreSlot func(context.Context, managedpostgres.RestoreSlotNotice) error
}

func (h *managedPostgresHooks) charges() billing.ManagedPostgresChargeSink {
	if h == nil || h.Charges == nil {
		return nil
	}
	return h.Charges()
}

func (h *managedPostgresHooks) restoreSlotExpired() func(context.Context, managedpostgres.Database) {
	if h == nil {
		return nil
	}
	return h.RestoreSlotExpired
}

func (h *managedPostgresHooks) notifyRestoreSlot() func(context.Context, managedpostgres.RestoreSlotNotice) error {
	if h == nil {
		return nil
	}
	return h.NotifyRestoreSlot
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
	registry, err := managedpostgres.Load(getenv, managedPostgresFactories(pool))
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
	var fleet *managedpostgres.Service
	capLimits := managedPostgresCapLimits(store, accountStore, overage)
	usageOptions.AfterSweep = func(ctx context.Context) {
		if _, err := capEnforcer.Enforce(ctx); err != nil && ctx.Err() == nil {
			log.Warn("managed postgres compute cap sweep failed", "error", err)
		}
		deliverManagedPostgresOverage(ctx, store, hooks.charges(), capLimits, log)
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
			admitErr := registry.UsagePolicy().AdmitWithCeilings(ctx, store, accountID, time.Now().UTC(), managedPostgresUsageCeilings(limits, overageOn))
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
	restoreSlots, err := managedpostgres.NewRestoreSlotSweeper(service, bindingService, managedpostgres.RestoreSlotSweeperOptions{
		Expired: hooks.restoreSlotExpired(),
		Notify:  hooks.notifyRestoreSlot(),
		Logger:  log,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	reconciler.SetRestoreSlots(restoreSlots)
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
		Ceiling:     managedPostgresCapLimits(store, accountStore, hooks.overage()),
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

// managedPostgresCapLimits returns a subject's compute limits: its plan's
// monthly allowance, and the ceiling, twice it with overage on. A deleted
// subject is managedpostgres.ErrNotFound.
func managedPostgresCapLimits(store managedpostgres.OverageStore, accountStore state.AppOwnerReader, overage managedPostgresOverageGate) managedpostgres.CapCeiling {
	return func(ctx context.Context, subject string) (managedpostgres.CapLimits, error) {
		account, err := state.BillingResourceAccount(ctx, accountStore, subject)
		if errors.Is(err, state.ErrNotFound) {
			return managedpostgres.CapLimits{}, managedpostgres.ErrNotFound
		}
		if err != nil {
			return managedpostgres.CapLimits{}, err
		}
		limits, _ := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
		overageOn, err := managedPostgresStoredOverage(ctx, store, overage, account, subject)
		if err != nil {
			return managedpostgres.CapLimits{}, err
		}
		return managedpostgres.CapLimits{Allowance: limits.MonthlyComputeUnitSeconds,
			Ceiling: managedPostgresComputeCeiling(limits, overageOn), Overage: overageOn,
			StoragePool: managedPostgresStoragePool(limits, overageOn)}, nil
	}
}

// deliverManagedPostgresOverage charges overage through the billing provider
// after a cap sweep (ADR-20261031110037). Without a provider, or with its
// managed PostgreSQL policy off, nothing is charged or recorded.
func deliverManagedPostgresOverage(ctx context.Context, store managedpostgres.UsageStore, sink billing.ManagedPostgresChargeSink, limits managedpostgres.CapCeiling, log *slog.Logger) {
	if sink == nil {
		return
	}
	policy, ok := sink.ManagedPostgresChargePolicy()
	if !ok {
		return
	}
	options := managedpostgres.OverageBillerOptions{Provider: policy.Provider, EffectiveFrom: policy.EffectiveFrom, Allowance: limits, Logger: log}
	if policy.Live {
		options.Publish = func(ctx context.Context, step managedpostgres.OverageChargeStep) error {
			return sink.PublishManagedPostgresCharge(ctx, billing.ManagedPostgresChargeStep(step))
		}
	}
	biller, err := managedpostgres.NewOverageBiller(store, options)
	if err != nil {
		log.Warn("managed postgres overage billing misconfigured", "error", err)
		return
	}
	summary, err := biller.Deliver(ctx)
	if err != nil && ctx.Err() == nil {
		log.Warn("managed postgres overage delivery failed", "error", err, "subjects", summary.Subjects, "failed", summary.Failed)
	}
}

// configureManagedPostgresCapMode records the compute cap enforcement mode for
// the usage views. loadManagedPostgres already refused an invalid value.
func (s *server) configureManagedPostgresCapMode(getenv func(string) string) {
	s.managedPostgresCapMode, _ = managedpostgres.ParseCapEnforcementMode(getenv(managedpostgres.CapEnforcementEnv))
}

// managedPostgresStoredOverage reads a subject's overage choice when the
// account may use overage at all.
func managedPostgresStoredOverage(ctx context.Context, store managedpostgres.OverageStore, overage managedPostgresOverageGate, account state.Account, subject string) (bool, error) {
	if overage == nil || !overage(account) {
		return false, nil
	}
	return store.OverageEnabled(ctx, subject)
}

func managedPostgresUsageCeilings(limits api.ManagedPostgresPlanLimits, overage bool) managedpostgres.UsageCeilings {
	if limits.DatabasesMax <= 0 {
		return managedpostgres.UsageCeilings{}
	}
	const secondsPerBillingMonth = int64(31 * 24 * time.Hour / time.Second)
	ceilings := managedpostgres.UsageCeilings{}
	if limits.MonthlyComputeUnitSeconds > 0 {
		ceilings.MaxMonthlyComputeUnitSeconds = managedPostgresComputeCeiling(limits, overage)
	}
	if limits.StorageLimitBytes > 0 && limits.StorageLimitBytes <= math.MaxInt64/secondsPerBillingMonth {
		ceilings.MaxMonthlyStorageByteSeconds = limits.StorageLimitBytes * secondsPerBillingMonth
	}
	return ceilings
}

// upgrades and storageResizes are read per request: apid tests build the
// handler first.
func (s *server) upgrades() managedpg.Rollout       { return s.postgresUpgrades }
func (s *server) storageResizes() managedpg.Rollout { return s.postgresStorage }

// managedPostgresResize serves a resize route with the service as
// configured when the request arrives.
func (s *server) managedPostgresResize(serve func(managedpg.Resizes, http.ResponseWriter, *http.Request, state.Account)) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		rs := managedpg.Resizes{Storage: s.storageResizes, Problem: managedPostgresProblem, NotConfigured: managedPostgresNotConfiguredProblem, Audit: s.audit}
		if s.managedPostgres != nil {
			rs.Service = s.managedPostgres
		}
		serve(rs, w, r, acct)
	}
}

// managedPostgresUpgrade serves a major-upgrade route with the service as
// configured when the request arrives.
func (s *server) managedPostgresUpgrade(serve func(managedpg.Upgrades, http.ResponseWriter, *http.Request, state.Account)) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		u := managedpg.Upgrades{Problem: managedPostgresProblem, NotConfigured: managedPostgresNotConfiguredProblem, Audit: s.audit}
		if s.managedPostgres != nil {
			u.Service = s.managedPostgres
		}
		serve(u, w, r, acct)
	}
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
