package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureDurableEntities(ctx context.Context, getenv func(string) string) error {
	artifacts, artifactErr := validatorbundle.OpenArtifacts(getenv)
	if artifactErr != nil {
		return artifactErr
	}
	if artifacts != nil && (getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_VALIDATOR_RELEASE_GATE_ENABLED") != "1") {
		return errors.New("shared validator artifacts require durable entities, isolated validation and validator release preflight")
	}
	s.durableEntityValidatorArtifacts = artifacts
	releaseGate := getenv("FAAS_DURABLE_ENTITY_VALIDATOR_RELEASE_GATE_ENABLED") == "1"
	if releaseGate && getenv("FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED") != "1" {
		return errors.New("validator release gate requires isolated restore validation")
	}
	s.durableEntityValidatorReleaseGateEnabled = releaseGate
	isolation := getenv("FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED") == "1"
	if isolation {
		if getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" || getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED") != "1" || !s.executionAPIEnabled {
			return errors.New("isolated restore validation requires durable entities, restore validation and disposable executions")
		}
		if artifacts == nil {
			bundles, err := loadDurableEntityValidatorBundles(getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATOR_BUNDLES_FILE"))
			if err != nil {
				return err
			}
			s.durableEntityValidatorBundles = bundles
		}
	}
	s.durableEntityRestoreIsolationEnabled = isolation
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
	backend, err := s.objectStorage.Resolve(getenv("FAAS_DURABLE_ENTITY_BACKEND"), getenv("FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT"))
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
	// The request budget is shorter than this lease. We deliberately avoid a
	// renewal writer racing every transition in the first synchronous slice.
	probeCtx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	observed := durableEntityObservedStore{ObjectStore: store, metrics: func() *durableEntityMetrics { return s.durableEntityMetrics }}
	engine, err := durableentity.Open(probeCtx, observed, durableentity.Options{LeaseDuration: api.MaxDurableEntityLease, RetainedBytesLimit: limit})
	if err != nil {
		return errors.New("durable entities require private conditional writes and strongly consistent reads")
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
		if _, ok := s.store.(state.EntityOutboxDeliveryStore); !ok {
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
	s.durableEntityRestoreValidationEnabled = getenv("FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED") == "1"
	s.durableEntityBackupsEnabled = backups
	s.durableEntityHealthEnabled = health
	s.durableEntities, s.durableEntityApps, s.durableEntityOwner = engine, apps, uuid.NewString()
	s.durableEntityAlarmsEnabled = alarms
	s.durableEntityMaintenanceEnabled = maintenance
	s.durableEntityOutboxEnabled = outbox
	s.durableEntityOutboxHandlersEnabled = handlers
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
