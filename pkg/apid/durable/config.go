package durable

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) Configure(ctx context.Context, getenv func(string) string, objects *objectstorage.Registry) error {
	s.Sockets = nil
	s.TenantQuotas = nil
	s.QuotaMigrationEnabled = false
	s.QuotaPolicyEnabled = false
	s.RetirementEnabled = false
	s.RegistryEnabled = false
	s.RegistryCleanupEnabled = false
	s.RegistryCleanupWorkerEnabled = false
	quotas := getenv("FAAS_DURABLE_ENTITY_TENANT_QUOTAS_ENABLED") == "1"
	migration := getenv("FAAS_DURABLE_ENTITY_QUOTA_MIGRATION_ENABLED") == "1"
	policy := getenv("FAAS_DURABLE_ENTITY_QUOTA_POLICY_ENABLED") == "1"
	registry := getenv("FAAS_DURABLE_ENTITY_REGISTRY_ENABLED") == "1"
	registryCleanup := getenv("FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_ENABLED") == "1"
	registryWorker := getenv("FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_WORKER_ENABLED") == "1"
	if registryWorker && !registryCleanup {
		return errors.New("registry cleanup worker requires registry cleanup management")
	}
	if registryCleanup && !registry {
		return errors.New("registry cleanup requires registry management")
	}
	if registry && !policy {
		return errors.New("entity registry requires quota policy management")
	}
	retirement := getenv("FAAS_DURABLE_ENTITY_RETIREMENT_ENABLED") == "1"
	if retirement && !policy {
		return errors.New("entity retirement requires quota policy management")
	}
	if policy && !migration {
		return errors.New("quota policy changes require quota migration")
	}
	if migration && !quotas {
		return errors.New("quota migration requires tenant quotas")
	}
	if quotas && getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" {
		return errors.New("tenant quotas require durable entities")
	}
	s.SessionGrants = nil
	browser := getenv("FAAS_DURABLE_ENTITY_BROWSER_SESSIONS_ENABLED") == "1"
	if browser && getenv("FAAS_DURABLE_ENTITY_SOCKETS_ENABLED") != "1" {
		return errors.New("browser sessions require entity sockets")
	}
	sockets := getenv("FAAS_DURABLE_ENTITY_SOCKETS_ENABLED") == "1"
	if sockets && getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" {
		return errors.New("entity sockets require durable entities")
	}
	artifacts, artifactErr := validatorbundle.OpenArtifacts(getenv)
	if artifactErr != nil {
		return artifactErr
	}
	if artifacts != nil && (getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_VALIDATOR_RELEASE_GATE_ENABLED") != "1") {
		return errors.New("shared validator artifacts require durable entities, isolated validation and validator release preflight")
	}
	s.ValidatorArtifacts = artifacts
	releaseGate := getenv("FAAS_DURABLE_ENTITY_VALIDATOR_RELEASE_GATE_ENABLED") == "1"
	if releaseGate && getenv("FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED") != "1" {
		return errors.New("validator release gate requires isolated restore validation")
	}
	s.ValidatorReleaseGateEnabled = releaseGate
	isolation := getenv("FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED") == "1"
	if isolation {
		if getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED") != "1" || !s.executionAPIEnabled() {
			return errors.New("isolated restore validation requires durable entities, restore validation and disposable executions")
		}
		if artifacts == nil {
			bundles, err := loadDurableEntityValidatorBundles(getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATOR_BUNDLES_FILE"))
			if err != nil {
				return err
			}
			s.ValidatorBundles = bundles
		}
	}
	s.RestoreIsolationEnabled = isolation
	handlers := getenv("FAAS_DURABLE_ENTITY_OUTBOX_HANDLERS_ENABLED") == "1"
	if handlers && (getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_OUTBOX_ENABLED") != "1") {
		return errors.New("durable entity outbox handlers require the invocation preview and outbox relay")
	}
	if getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" {
		if getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED") == "1" {
			return errors.New("durable entity restore validation requires the invocation preview")
		}
		if getenv("FAAS_DURABLE_ENTITY_BACKUPS_ENABLED") == "1" {
			return errors.New("durable entity backups require the invocation preview")
		}
		if getenv("FAAS_DURABLE_ENTITY_HEALTH_ENABLED") == "1" {
			return errors.New("durable entity health requires the invocation preview")
		}
		if getenv("FAAS_DURABLE_ENTITY_OUTBOX_ENABLED") == "1" {
			return errors.New("durable entity outbox requires the invocation preview")
		}
		if getenv("FAAS_DURABLE_ENTITY_ALARMS_ENABLED") == "1" {
			return errors.New("durable entity alarms require the invocation preview")
		}
		if getenv("FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED") == "1" {
			return errors.New("durable entity maintenance requires the invocation preview")
		}
		return nil
	}
	backend, err := objects.Resolve(getenv("FAAS_DURABLE_ENTITY_BACKEND"), getenv("FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT"))
	if err != nil {
		return errors.New("select a configured backend and its placement fingerprint")
	}
	provider, ok := backend.Provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		return durableentity.ErrUnsupported
	}
	store, err := durableentity.NewProviderStore(provider, getenv("FAAS_DURABLE_ENTITY_BUCKET"))
	if err != nil {
		return err
	}
	apps, err := durableEntityAppAllowlist(getenv("FAAS_DURABLE_ENTITY_APPS"))
	if err != nil {
		return err
	}
	limit, err := durableEntityStorageLimit(getenv("FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES"))
	if err != nil {
		return err
	}
	var tenantQuotas *entitysocket.Quotas
	if quotas {
		limits, err := TenantLimits(getenv, limit)
		if err != nil {
			return err
		}
		cache, err := entitysocket.NewRegistryCache(store, 8<<20, 256, 16)
		if err != nil {
			return err
		}
		tenantQuotas = &entitysocket.Quotas{Store: store, Limits: limits, RegistryCache: cache}
	}
	if registryCleanup {
		if err := tenantQuotas.CheckRegistryCleanup(ctx); err != nil {
			return errors.New("registry cleanup requires verified private listing and delete permission")
		}
	}
	if registryWorker {
		if err := tenantQuotas.CheckRegistryCleanupWorker(ctx); err != nil {
			return errors.New("registry cleanup worker requires verified quota discovery and private sidecar CAS permissions")
		}
		if err := tenantQuotas.CheckRegistryCleanupIndex(ctx); err != nil {
			return errors.New("registry cleanup worker requires verified private index permissions")
		}
	}
	// The request budget is shorter than this lease. We deliberately avoid a
	// renewal writer racing every transition in the first synchronous slice.
	probeCtx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	observed := observedStore{ObjectStore: store, metrics: func() *Metrics { return s.Metrics }}
	engine, err := durableentity.Open(probeCtx, observed, durableentity.Options{LeaseDuration: api.MaxDurableEntityLease, RetainedBytesLimit: limit})
	if err != nil {
		return errors.New("durable entities require private conditional writes and strongly consistent reads")
	}
	if migration {
		if err := engine.CheckHealthDiscovery(probeCtx); err != nil {
			return errors.New("quota migration requires bounded private delimiter listing")
		}
	}
	if retirement {
		if err := engine.CheckMaintenance(probeCtx); err != nil {
			return errors.New("entity retirement requires bounded private listing and verified delete permission")
		}
	}
	alarms := getenv("FAAS_DURABLE_ENTITY_ALARMS_ENABLED") == "1"
	if alarms {
		if err := engine.CheckAlarmDiscovery(probeCtx); err != nil {
			return errors.New("durable entity alarms require private delimiter/flat listing and hint deletion")
		}
	}
	maintenance := getenv("FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED") == "1"
	if maintenance {
		if err := engine.CheckMaintenance(probeCtx); err != nil {
			return errors.New("durable entity maintenance requires private delimiter/flat listing and deletion")
		}
	}
	outbox := getenv("FAAS_DURABLE_ENTITY_OUTBOX_ENABLED") == "1"
	if outbox {
		if _, ok := s.Host.Store().(state.EntityOutboxDeliveryStore); !ok {
			return errors.New("durable entity outbox requires deduplicating webhook acceptance")
		}
		if err := engine.CheckOutboxDiscovery(probeCtx); err != nil {
			return errors.New("durable entity outbox requires private delimiter/flat listing and hint deletion")
		}
	}
	health := getenv("FAAS_DURABLE_ENTITY_HEALTH_ENABLED") == "1"
	if health {
		if err := engine.CheckHealthDiscovery(probeCtx); err != nil {
			return errors.New("durable entity health requires private delimiter listing")
		}
	}
	backups := getenv("FAAS_DURABLE_ENTITY_BACKUPS_ENABLED") == "1"
	if backups {
		if err := engine.CheckBackups(probeCtx); err != nil {
			return errors.New("durable entity backups require private listing and deletion")
		}
	}
	if sockets {
		s.Sockets = entitysocket.New()
		if browser {
			s.SessionGrants = &entitysocket.Grants{Store: store, Quotas: tenantQuotas}
		}
	}
	s.RestoreValidationEnabled = getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED") == "1"
	s.BackupsEnabled = backups
	s.HealthEnabled = health
	s.TenantQuotas = tenantQuotas
	s.QuotaMigrationEnabled = migration
	s.QuotaPolicyEnabled = policy
	s.RetirementEnabled = retirement
	s.RegistryEnabled = registry
	s.RegistryCleanupEnabled = registryCleanup
	s.RegistryCleanupWorkerEnabled = registryWorker
	s.Engine, s.Apps, s.Owner = engine, apps, uuid.NewString()
	s.AlarmsEnabled = alarms
	s.MaintenanceEnabled = maintenance
	s.OutboxEnabled = outbox
	s.OutboxHandlersEnabled = handlers
	return nil
}

func durableEntityStorageLimit(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 || strconv.FormatInt(limit, 10) != raw {
		return 0, errors.New("FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES must be an explicit positive byte count")
	}
	return limit, nil
}

func durableEntityAppAllowlist(raw string) (map[string]bool, error) {
	apps := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil || id == uuid.Nil {
			return nil, errors.New("FAAS_DURABLE_ENTITY_APPS must contain explicit app UUIDs")
		}
		apps[id.String()] = true
	}
	return apps, nil
}
